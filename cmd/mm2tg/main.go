// STYLiTE Orbit Mattermost to Telegrambot: one-way Mattermost -> Telegram firehose.
//
// Listens to every Mattermost event the logged-in user can see
// (public, private, DM, group DM) and forwards a formatted line
// to a single Telegram chat. Read-only: no reply path.
//
// Talks to Mattermost directly through github.com/mattermost/mattermost/server/public/model
// (Client4 + WebSocketClient) — no matterclient wrapper.
//
// Env:
//
//	MM_SERVER    host of the Mattermost server, e.g. chat.example.com
//	MM_SCHEME    https or http (default https)
//	MM_LOGIN     username or email
//	MM_PASS      password (for token auth, use "token=<PAT>")
//	MM_MFA       optional MFA token
//	TG_TOKEN     Telegram bot token
//	TG_CHAT_ID   target chat id (int64)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/mattermost/mattermost/server/public/model"
)

func env(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing env %s", k)
	}
	return v
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type bridge struct {
	httpURL string
	wsURL   string
	login   string
	pass    string
	mfa     string

	bot      *tgbotapi.BotAPI
	tgChat   int64
	logInfo  bool
}

func main() {
	server := env("MM_SERVER")
	scheme := envOr("MM_SCHEME", "https")
	wsScheme := "wss"
	if scheme == "http" {
		wsScheme = "ws"
	}

	level := strings.ToLower(envOr("MM_LOGLEVEL", "info"))
	b := &bridge{
		httpURL: scheme + "://" + server,
		wsURL:   wsScheme + "://" + server,
		login:   env("MM_LOGIN"),
		pass:    env("MM_PASS"),
		mfa:     os.Getenv("MM_MFA"),
		logInfo: level == "info" || level == "debug",
	}

	tgChat, err := strconv.ParseInt(env("TG_CHAT_ID"), 10, 64)
	if err != nil {
		log.Fatalf("TG_CHAT_ID: %v", err)
	}
	b.tgChat = tgChat

	bot, err := tgbotapi.NewBotAPI(env("TG_TOKEN"))
	if err != nil {
		log.Fatalf("telegram: %v", err)
	}
	b.bot = bot

	log.Printf("STYLiTE Orbit Mattermost to Telegrambot starting up")

	backoff := newBackoff()
	for {
		if err := b.session(); err != nil {
			d := backoff.next()
			log.Printf("session ended: %v — reconnecting in %s", err, d)
			time.Sleep(d)
			continue
		}
		backoff.reset()
	}
}

// session performs one login + websocket lifecycle. Returns when the
// session needs to be re-established.
func (b *bridge) session() error {
	c4 := model.NewAPIv4Client(b.httpURL)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	user, resp, err := b.authenticate(ctx, c4)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	token := resp.Header.Get(model.HeaderToken)
	if token == "" {
		token = c4.AuthToken
	}
	if token == "" {
		return fmt.Errorf("login: no auth token in response")
	}
	c4.SetToken(token)

	log.Printf("STYLiTE Orbit: logged in as %s, forwarding to telegram chat %d", user.Username, b.tgChat)

	if err := primeSession(ctx, c4, user.Id); err != nil {
		log.Printf("prime session warning: %v", err)
	}

	ws, wsErr := model.NewWebSocketClient4(b.wsURL, token)
	if wsErr != nil {
		return fmt.Errorf("ws connect: %v", wsErr)
	}
	defer ws.Close()
	ws.Listen()

	log.Printf("WS connected to %s", b.wsURL)

	// Mirror the Mattermost web client: request presence subscription right
	// after connect. Without any outbound WS traffic the server closes bots
	// as idle around 30s.
	ws.GetStatuses()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	heartbeat := time.NewTicker(60 * time.Second)
	defer heartbeat.Stop()

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	var events int
	for {
		select {
		case ev, ok := <-ws.EventChannel:
			if !ok {
				return fmt.Errorf("event channel closed after %d events", events)
			}
			events++
			b.handle(c4, ev)
		case resp, ok := <-ws.ResponseChannel:
			if !ok {
				return fmt.Errorf("response channel closed after %d events", events)
			}
			if resp != nil && resp.Status != "OK" {
				log.Printf("WS server response status=%s error=%v", resp.Status, resp.Error)
			}
		case <-ws.PingTimeoutChannel:
			return fmt.Errorf("ping timeout after %d events", events)
		case <-ticker.C:
			if ws.ListenError != nil {
				return fmt.Errorf("listen error: %v", ws.ListenError)
			}
		case <-keepalive.C:
			ws.GetStatuses()
		case <-heartbeat.C:
			log.Printf("WS alive: %d events received", events)
		}
	}
}

// primeSession makes HTTP calls that Mattermost treats as "real activity" for
// the session, mirroring what matterclient / the official webapp do after
// login and before opening the websocket. Without this, sessions can be
// treated as idle and the server closes the WS ~30s later.
func primeSession(ctx context.Context, c4 *model.Client4, userID string) error {
	teams, _, err := c4.GetTeamsForUser(ctx, userID, "")
	if err != nil {
		return fmt.Errorf("GetTeamsForUser: %w", err)
	}
	for _, t := range teams {
		if _, _, err := c4.GetChannelsForTeamForUser(ctx, t.Id, userID, false, ""); err != nil {
			return fmt.Errorf("GetChannelsForTeamForUser(%s): %w", t.Name, err)
		}
	}
	return nil
}

// authenticate handles password, token=<PAT>, and MFA inputs.
func (b *bridge) authenticate(ctx context.Context, c4 *model.Client4) (*model.User, *model.Response, error) {
	if strings.HasPrefix(b.pass, "token=") {
		token := strings.TrimPrefix(b.pass, "token=")
		c4.SetToken(token)
		user, resp, err := c4.GetMe(ctx, "")
		return user, resp, err
	}
	if b.mfa != "" {
		return c4.LoginWithMFA(ctx, b.login, b.pass, b.mfa)
	}
	return c4.Login(ctx, b.login, b.pass)
}

func (b *bridge) handle(c4 *model.Client4, ev *model.WebSocketEvent) {
	if ev.EventType() != model.WebsocketEventPosted {
		return
	}

	data := ev.GetData()
	postJSON, _ := data["post"].(string)
	if postJSON == "" {
		return
	}
	var post model.Post
	if err := json.Unmarshal([]byte(postJSON), &post); err != nil {
		return
	}
	if strings.TrimSpace(post.Message) == "" {
		return
	}

	chType, _ := data["channel_type"].(string)
	chName, _ := data["channel_display_name"].(string)
	if chName == "" {
		chName, _ = data["channel_name"].(string)
	}
	sender, _ := data["sender_name"].(string)
	sender = strings.TrimPrefix(sender, "@")
	teamID := ev.GetBroadcast().TeamId
	teamName := teamID
	if teamID == "" {
		teamName = "-"
	}

	line := fmt.Sprintf("[%s %s/%s] <%s> %s", labelType(chType), teamName, chName, sender, post.Message)
	b.send(line)

	if b.logInfo {
		log.Printf("forwarded: type=%s team=%s channel=%s sender=%s len=%d",
			labelType(chType), teamName, chName, sender, len(post.Message))
	}
}

func labelType(t string) string {
	switch t {
	case "O":
		return "PUB"
	case "P":
		return "PRIV"
	case "D":
		return "DM"
	case "G":
		return "GDM"
	default:
		return "?"
	}
}

func (b *bridge) send(text string) {
	const max = 3900
	for len(text) > 0 {
		chunk := text
		if len(chunk) > max {
			chunk = chunk[:max]
		}
		text = text[len(chunk):]
		msg := tgbotapi.NewMessage(b.tgChat, chunk)
		msg.DisableNotification = true
		if _, err := b.bot.Send(msg); err != nil {
			log.Printf("telegram send: %v", err)
			time.Sleep(2 * time.Second)
		}
	}
}

type backoff struct {
	cur, min, max time.Duration
}

func newBackoff() *backoff { return &backoff{min: 2 * time.Second, max: 2 * time.Minute} }

func (b *backoff) next() time.Duration {
	if b.cur == 0 {
		b.cur = b.min
		return b.cur
	}
	b.cur *= 2
	if b.cur > b.max {
		b.cur = b.max
	}
	return b.cur
}

func (b *backoff) reset() { b.cur = 0 }
