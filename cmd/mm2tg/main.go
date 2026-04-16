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
//	MM_SERVER             host of the Mattermost server, e.g. chat.example.com
//	MM_SCHEME             https or http (default https)
//	MM_LOGIN              username or email
//	MM_PASS               password (for token auth, use "token=<PAT>")
//	MM_MFA                optional MFA token
//	MM_SKIP_OWN           skip messages from the logged-in user (default "true")
//	MM_CHANNEL_WHITELIST  comma-separated regexes; only forward matching channels
//	MM_CHANNEL_BLACKLIST  comma-separated regexes; drop matching channels
//	MM_SENDER_WHITELIST   comma-separated regexes; only forward matching senders
//	MM_SENDER_BLACKLIST   comma-separated regexes; drop matching senders
//	TG_TOKEN              Telegram bot token
//	TG_CHAT_ID            target chat id (int64)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/mattermost/mattermost/server/public/model"
)

var (
	version   = "dev"
	gitHash   = "unknown"
	buildTime = "unknown"
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

// regexFilter holds compiled whitelist and blacklist patterns.
// Whitelist is checked first: if non-empty, the value must match at
// least one pattern. Then blacklist: if it matches any pattern, it is
// dropped.
type regexFilter struct {
	whitelist []*regexp.Regexp
	blacklist []*regexp.Regexp
}

// match returns true if the value passes the filter.
func (f regexFilter) match(value string) bool {
	if len(f.whitelist) > 0 {
		allowed := false
		for _, re := range f.whitelist {
			if re.MatchString(value) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	for _, re := range f.blacklist {
		if re.MatchString(value) {
			return false
		}
	}
	return true
}

// parseRegexFilter parses a comma-separated list of regex patterns from
// an env var value. Returns nil slice if the string is empty. Fatals on
// invalid regex.
func parseRegexFilter(envKey, raw string) []*regexp.Regexp {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]*regexp.Regexp, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			log.Fatalf("%s: invalid regex %q: %v", envKey, p, err)
		}
		out = append(out, re)
	}
	return out
}

type bridge struct {
	httpURL  string
	wsURL    string
	login    string
	pass     string
	mfa      string
	username string // logged-in Mattermost username, set after auth

	bot     *tgbotapi.BotAPI
	tgChat  int64
	logInfo bool
	skipOwn bool

	channelFilter regexFilter
	senderFilter  regexFilter

	teamNames map[string]string
}

func main() {
	server := env("MM_SERVER")
	scheme := envOr("MM_SCHEME", "https")
	wsScheme := "wss"
	if scheme == "http" {
		wsScheme = "ws"
	}

	level := strings.ToLower(envOr("MM_LOGLEVEL", "info"))
	skipOwn := strings.ToLower(envOr("MM_SKIP_OWN", "true")) != "false"
	b := &bridge{
		httpURL:   scheme + "://" + server,
		wsURL:     wsScheme + "://" + server,
		login:     env("MM_LOGIN"),
		pass:      env("MM_PASS"),
		mfa:       os.Getenv("MM_MFA"),
		logInfo:   level == "info" || level == "debug",
		skipOwn:   skipOwn,
		channelFilter: regexFilter{
			whitelist: parseRegexFilter("MM_CHANNEL_WHITELIST", os.Getenv("MM_CHANNEL_WHITELIST")),
			blacklist: parseRegexFilter("MM_CHANNEL_BLACKLIST", os.Getenv("MM_CHANNEL_BLACKLIST")),
		},
		senderFilter: regexFilter{
			whitelist: parseRegexFilter("MM_SENDER_WHITELIST", os.Getenv("MM_SENDER_WHITELIST")),
			blacklist: parseRegexFilter("MM_SENDER_BLACKLIST", os.Getenv("MM_SENDER_BLACKLIST")),
		},
		teamNames: map[string]string{},
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

	log.Printf("STYLiTE Orbit Mattermost to Telegrambot version=%s commit=%s built=%s starting up",
		version, gitHash, buildTime)

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

	b.username = user.Username
	log.Printf("STYLiTE Orbit: logged in as %s, forwarding to telegram chat %d", user.Username, b.tgChat)

	if err := b.primeSession(ctx, c4, user.Id); err != nil {
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
// login and before opening the websocket. It also seeds the teamID → teamName
// cache used when formatting Telegram messages.
func (b *bridge) primeSession(ctx context.Context, c4 *model.Client4, userID string) error {
	teams, _, err := c4.GetTeamsForUser(ctx, userID, "")
	if err != nil {
		return fmt.Errorf("GetTeamsForUser: %w", err)
	}
	for _, t := range teams {
		b.teamNames[t.Id] = t.Name
		if _, _, err := c4.GetChannelsForTeamForUser(ctx, t.Id, userID, false, ""); err != nil {
			return fmt.Errorf("GetChannelsForTeamForUser(%s): %w", t.Name, err)
		}
	}
	return nil
}

// teamLabel resolves a team ID to its slug, caching misses via c4.GetTeam.
func (b *bridge) teamLabel(c4 *model.Client4, teamID string) string {
	if teamID == "" {
		return ""
	}
	if name, ok := b.teamNames[teamID]; ok {
		return name
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t, _, err := c4.GetTeam(ctx, teamID, "")
	if err != nil || t == nil {
		b.teamNames[teamID] = ""
		return ""
	}
	b.teamNames[teamID] = t.Name
	return t.Name
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

	// Skip own messages.
	if b.skipOwn && strings.EqualFold(sender, b.username) {
		return
	}

	// Apply sender filter.
	if !b.senderFilter.match(sender) {
		return
	}

	teamName := b.teamLabel(c4, ev.GetBroadcast().TeamId)

	// Apply channel filter. Match string is "team/channel" (or just
	// "channel" when there is no team, e.g. DMs).
	channelPath := chName
	if teamName != "" {
		channelPath = teamName + "/" + chName
	}
	if !b.channelFilter.match(channelPath) {
		return
	}

	location := html.EscapeString(chName)
	if teamName != "" {
		location = html.EscapeString(teamName) + "/" + location
	}
	header := fmt.Sprintf("%s <b>%s</b> · <i>%s</i>",
		typeIcon(chType), html.EscapeString(sender), location)
	body := html.EscapeString(post.Message)
	b.send(header + "\n" + body)

	if b.logInfo {
		logTeam := teamName
		if logTeam == "" {
			logTeam = "-"
		}
		log.Printf("forwarded: type=%s team=%s channel=%s sender=%s len=%d",
			labelType(chType), logTeam, chName, sender, len(post.Message))
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

func typeIcon(t string) string {
	switch t {
	case "O":
		return "#"
	case "P":
		return "#\U0001F512" // private channel: hash + tiny padlock
	case "D":
		return "\U0001F4AC" // 💬
	case "G":
		return "\U0001F465" // 👥
	default:
		return "\u2753" // ❓
	}
}

func (b *bridge) send(text string) {
	const max = 3900
	for len(text) > 0 {
		chunk, rest := splitForTelegram(text, max)
		text = rest
		msg := tgbotapi.NewMessage(b.tgChat, chunk)
		msg.DisableNotification = true
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = true
		if _, err := b.bot.Send(msg); err != nil {
			log.Printf("telegram send: %v", err)
			time.Sleep(2 * time.Second)
		}
	}
}

// splitForTelegram returns a prefix of s that is safe to send as one
// HTML-parse-mode Telegram message, and the remainder. It splits on
// newline or space when possible, always on a UTF-8 rune boundary,
// and never inside an `&entity;` sequence.
func splitForTelegram(s string, max int) (string, string) {
	if len(s) <= max {
		return s, ""
	}
	cut := max
	if nl := strings.LastIndexByte(s[:cut], '\n'); nl > max/2 {
		cut = nl + 1
	} else if sp := strings.LastIndexByte(s[:cut], ' '); sp > max/2 {
		cut = sp + 1
	}
	// back off if we land inside an HTML entity
	if amp := strings.LastIndexByte(s[:cut], '&'); amp >= 0 {
		if semi := strings.IndexByte(s[amp:], ';'); semi < 0 || amp+semi >= cut {
			cut = amp
		}
	}
	// back off to a UTF-8 rune boundary
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	if cut == 0 {
		cut = max
	}
	return s[:cut], s[cut:]
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
