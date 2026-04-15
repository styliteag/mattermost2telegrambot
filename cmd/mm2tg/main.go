// STYLiTE Orbit Mattermost to Telegrambot: one-way Mattermost -> Telegram firehose.
//
// Listens to every Mattermost event the logged-in user can see
// (public, private, DM, group DM) and forwards a formatted line
// to a single Telegram chat. Read-only: no reply path.
//
// Env:
//
//	MM_SERVER    e.g. chat.example.com
//	MM_TEAM      team name (URL slug)
//	MM_LOGIN     username or email
//	MM_PASS      password or personal access token
//	MM_MFA       optional MFA token
//	MM_LOGLEVEL  matterclient log level (debug|info|warn); default info
//	TG_TOKEN     Telegram bot token
//	TG_CHAT_ID   target chat id (int64, negative for groups)
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/matterbridge/matterclient"
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

func main() {
	server := env("MM_SERVER")
	team := env("MM_TEAM")
	login := env("MM_LOGIN")
	pass := env("MM_PASS")
	mfa := os.Getenv("MM_MFA")
	logLevel := envOr("MM_LOGLEVEL", "info")

	tgToken := env("TG_TOKEN")
	tgChat, err := strconv.ParseInt(env("TG_CHAT_ID"), 10, 64)
	if err != nil {
		log.Fatalf("TG_CHAT_ID: %v", err)
	}

	bot, err := tgbotapi.NewBotAPI(tgToken)
	if err != nil {
		log.Fatalf("telegram: %v", err)
	}

	mc := matterclient.New(login, pass, team, server, mfa)
	mc.SetLogLevel(logLevel)
	log.Printf("STYLiTE Orbit MatterMost to Telegrambot starting up")
	if err := mc.Login(); err != nil {
		log.Fatalf("mattermost login: %v", err)
	}
	log.Printf("STYLiTE Orbit: logged in as %s, forwarding to telegram chat %d", mc.User.Username, tgChat)

	for msg := range mc.MessageChan {
		line := format(mc, msg)
		if line == "" {
			continue
		}
		send(bot, tgChat, line)
	}
	log.Fatal("matterclient MessageChan closed — exiting")
}

func format(mc *matterclient.Client, m *matterclient.Message) string {
	if m.Post == nil || strings.TrimSpace(m.Text) == "" {
		return ""
	}
	chType := "?"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ch, _, err := mc.Client.GetChannel(ctx, m.Post.ChannelId, "")
	cancel()
	if err == nil && ch != nil {
		switch ch.Type {
		case model.ChannelTypeDirect:
			chType = "DM"
		case model.ChannelTypeGroup:
			chType = "GDM"
		case model.ChannelTypePrivate:
			chType = "PRIV"
		case model.ChannelTypeOpen:
			chType = "PUB"
		}
	}
	chName := mc.GetChannelName(m.Post.ChannelId)
	if chName == "" {
		chName = m.Channel
	}
	teamName := m.Team
	if teamName == "" {
		teamName = "-"
	}
	return fmt.Sprintf("[%s %s/%s] <%s> %s", chType, teamName, chName, m.Username, m.Text)
}

func send(bot *tgbotapi.BotAPI, chat int64, text string) {
	const max = 3900 // stay below Telegram's 4096 cap
	for len(text) > 0 {
		chunk := text
		if len(chunk) > max {
			chunk = chunk[:max]
		}
		text = text[len(chunk):]
		msg := tgbotapi.NewMessage(chat, chunk)
		msg.DisableNotification = true
		if _, err := bot.Send(msg); err != nil {
			log.Printf("telegram send: %v", err)
			time.Sleep(2 * time.Second)
		}
	}
}
