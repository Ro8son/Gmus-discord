package bot

import (
	"bufio"
	"encoding/binary"
	"io"
	"log"
	"os/exec"
	"strconv"
	"time"

	discord "github.com/bwmarrin/discordgo"
	"layeh.com/gopus"
)

var (
	//s                *discordgo.Session
	AudioChannels    int    = 2
	AudioFrameRate   int    = 48000
	AudioFrameSize   int    = 960
	AudioBitrate     int    = 128
	AudioApplication string = "voip"
	MaxBytes         int    = (AudioFrameSize * AudioChannels) * 2
	//OpusEncoder      *gopus.Encoder

	// EncodeChan chan []int16
	// OutputChan chan []byte

	// ffmpeg_stream io.ReadCloser
	// target        int
	// queue         []string
	// urls          []string
	// running       bool
	// loop          bool
)

type Bot struct {
	session *discord.Session

	GuildID string
	// channelID string

	opusEncoder *gopus.Encoder

	encodeChan chan []int16
	outputChan chan []byte

	ffmpegStream io.ReadCloser
	plr          Player
}

type Player struct {
	current int
	Queue   []string
	Titles  []string
	urls    []string
	playing bool
	loop    bool
}

func (bot *Bot) Init(s *discord.Session, guildID string) {
	var err error

	bot.session = s
	bot.GuildID = guildID

	bot.opusEncoder, err = gopus.NewEncoder(AudioFrameRate, AudioChannels, gopus.Voip)
	if err != nil {
		log.Println("NewEncoder Error:", err)
		return
	}

	if AudioBitrate < 1 || AudioBitrate > 512 {
		AudioBitrate = 64
	}
	bot.opusEncoder.SetBitrate(AudioBitrate * 1000)
	bot.opusEncoder.SetApplication(gopus.Voip)
}

func (bot *Bot) play(channelID string) {
	vc, err := bot.session.ChannelVoiceJoin(bot.GuildID, channelID, false, true)
	if err != nil {
		log.Println(err)
		return
		//return err
	}

	connect(vc)

	for bot.plr.current < len(bot.plr.Queue) {

		bot.encodeChan = make(chan []int16, 450)
		bot.outputChan = make(chan []byte, 450)

		url := bot.plr.Queue[bot.plr.current]
		//go get_title(url, m)

		bot.downloader(url)
		//time.Sleep(500 * time.Millisecond)
		go bot.reader()
		go bot.encoder()

		bot.play_sound(vc)

		bot.plr.current++
		if bot.plr.current >= len(bot.plr.Queue) && bot.plr.loop {
			bot.plr.current = 0
		}
	}

	bot.plr.Queue = bot.plr.Queue[:0]
	bot.plr.Titles = bot.plr.Titles[:0]
	bot.plr.current = 0

	disconnect(vc)
}

func (bot *Bot) downloader(url string) {
	cmd := exec.Command("yt-dlp", "-f", "251", "-o", "-", url)
	ffmpeg_cmd := exec.Command("ffmpeg", "-i", "pipe:0", "-f", "s16le", "-acodec", "pcm_s16le", "pipe:1")
	ffmpeg_cmd.Stdin, _ = cmd.StdoutPipe()
	bot.ffmpegStream, _ = ffmpeg_cmd.StdoutPipe()

	// Start yt-dlp command
	if err := cmd.Start(); err != nil {
		log.Println("Error starting yt-dlp command:", err)
		return
	}

	// Start ffmpeg command
	if err := ffmpeg_cmd.Start(); err != nil {
		log.Println("Error starting ffmpeg command:", err)
		return
	}

}

func (bot *Bot) reader() {
	var err error
	defer func() {
		close(bot.encodeChan)
	}()

	stdin := bufio.NewReaderSize(bot.ffmpegStream, 16384)

	for {
		buf := make([]int16, AudioFrameSize*AudioChannels)

		err = binary.Read(stdin, binary.LittleEndian, &buf)
		if err == io.EOF {
			// Okay! There's nothing left, time to quit.
			return
		}

		if err == io.ErrUnexpectedEOF {
			// Well there's just a tiny bit left, lets encode it, then quit.
			//EncodeChan <- buf
			return
		}

		if err != nil {
			// Oh no, something went wrong!
			log.Println("error reading from stdin,", err)
			return
		}

		// write pcm data to the EncodeChan
		bot.encodeChan <- buf
	}
}

func (bot *Bot) encoder() {

	defer func() {
		close(bot.outputChan)
	}()

	for {
		pcm, ok := <-bot.encodeChan
		if !ok {
			// if chan closed, exit
			return
		}

		// try encoding pcm frame with Opus
		opus, err := bot.opusEncoder.Encode(pcm, AudioFrameSize, MaxBytes)
		if err != nil {
			log.Println("Encoding Error:", err)
			return
		}

		// write opus data to OutputChan
		bot.outputChan <- opus
	}
}

// playSound plays the current buffer to the provided channel.
func (bot *Bot) play_sound(vc *discord.VoiceConnection) (err error) {

	bot.plr.playing = true
	for bot.plr.playing {
		opus, ok := <-bot.outputChan
		if !ok {
			bot.plr.playing = false
		}
		vc.OpusSend <- opus
	}

	return nil
}

func connect(vc *discord.VoiceConnection) {
	time.Sleep(100 * time.Millisecond)

	vc.Speaking(true)
}

func disconnect(vc *discord.VoiceConnection) {
	vc.Speaking(false)

	time.Sleep(100 * time.Millisecond)

	vc.Disconnect()
}

func (bot *Bot) Skip() {
	bot.plr.playing = false
}

func (bot *Bot) Loop() string {
	bot.plr.loop = !bot.plr.loop
	return strconv.FormatBool(bot.plr.loop)
}

func (bot *Bot) Play(url string, channelID string) {
	bot.plr.Queue = append(bot.plr.Queue, url)
	go bot.get_title(url)
	//bot.session.ChannelMessageSend(bot.channelID, "Added to queue: "+url)
	if len(bot.plr.Queue) == 1 {
		bot.play(channelID)
	}
}

func (bot *Bot) Len() int {
	return len(bot.plr.Queue)
}

func (bot *Bot) Clear() {
	bot.plr.Queue = bot.plr.Queue[:0]
	bot.plr.Titles = bot.plr.Titles[:0]
	bot.plr.loop = false
	bot.plr.playing = false
}

func (bot *Bot) Queue() []string {
	return bot.plr.Titles
}

func (bot *Bot) get_title(query string) {
	cmd := exec.Command("yt-dlp", query, "--get-title")

	output, err := cmd.Output()
	if err != nil {
		log.Println("Error executing command:", err)
	}

	output_string := string(output)

	bot.plr.Titles = append(bot.plr.Titles, output_string)
}
