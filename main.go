package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"tako/bot"

	discord "github.com/bwmarrin/discordgo"
)

var (
	botto *bot.Bot
	bots  []*bot.Bot
)

func init() {
	flag.StringVar(&token, "t", "", "Bot Token")
	flag.Parse()

}

var token string

func main() {

	if token == "" {
		log.Println("No token provided. Please run: airhorn -t <bot token>")
		return
	}

	dg, err := discord.New("Bot " + token)
	if err != nil {
		log.Println("Error creating Discord session: ", err)
		return
	}

	dg.AddHandler(ready)
	dg.AddHandler(message_create)

	dg.Identify.Intents = discord.IntentsGuilds | discord.IntentsGuildMessages | discord.IntentsGuildVoiceStates

	err = dg.Open()
	if err != nil {
		log.Println("Error opening Discord session: ", err)
	}

	// Wait here until CTRL-C or other term signal is received.
	log.Println("Takobot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Cleanly close down the Discord session.
	dg.Close()
}

func ready(s *discord.Session, event *discord.Ready) {
	s.UpdateGameStatus(0, "go.dev - search me")
}

func message_create(s *discord.Session, m *discord.MessageCreate) {

	if m.Author.ID == s.State.User.ID {
		return
	}

	c, err := s.State.Channel(m.ChannelID)
	if err != nil {
		// Could not find channel.
		return
	}

	// Find the guild for that channel.
	g, err := s.State.Guild(c.GuildID)
	if err != nil {
		// Could not find guild.
		return
	}

	var found = false

	for count := range bots {
		if bots[count].GuildID == g.ID {
			botto = bots[count]
			found = true
		}
	}

	if !found {
		botto = &bot.Bot{}
		botto.Init(s, g.ID)
		bots = append(bots, botto)
	}

	command, content, valid := strings.Cut(m.Content, " ")
	switch command {
	case "!skip":
		s.ChannelMessageSend(m.ChannelID, "Skipped")
		botto.Skip()
	case "!loop":
		if valid && content == "one" {
			s.ChannelMessageSend(m.ChannelID, "Loop: "+botto.Loop(1))
		}
		if !valid {
			s.ChannelMessageSend(m.ChannelID, "Loop: "+botto.Loop(0))
		}
	case "!queue":
		s.ChannelMessageSend(m.ChannelID, botto.Queue())
	case "!jump":
		if !valid {
			return
		}
		number, err := strconv.Atoi(content)
		if err != nil {
			log.Println(err)
			return
		}
		botto.Jump(number)
	case "!current":
		s.ChannelMessageSend(m.ChannelID, "Current: "+botto.Current())
	case "!clear":
		botto.Clear()
	case "!play":
		if !valid {
			return
		}
		for _, vs := range g.VoiceStates {
			if vs.UserID == m.Author.ID {
				s.ChannelMessageSend(m.ChannelID, "Added: "+content)
				botto.Play(content, vs.ChannelID)
			}
		}
		return
	case "!help":
		fallthrough
	default:
	}
}
