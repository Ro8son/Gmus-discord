package bot

import (
	"bufio"
	"encoding/binary"
	"io"
	"log"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	discord "github.com/bwmarrin/discordgo"
	"layeh.com/gopus"
)

var (
	AudioChannels    int    = 2
	AudioFrameRate   int    = 48000
	AudioFrameSize   int    = 960
	AudioBitrate     int    = 128
	AudioApplication string = "voip"
	MaxBytes         int    = (AudioFrameSize * AudioChannels) * 2
)

type Bot struct {
	session *discord.Session

	GuildID string
	// channelID string

	opusEncoder *gopus.Encoder
	yt_dlp      *exec.Cmd
	ffmpeg      *exec.Cmd

	encodeChan chan []int16
	outputChan chan []byte

	ffmpegStream io.ReadCloser
	queue        []*Title

	current int
	playing bool
	loop    bool
	loopOne bool
}

type Title struct {
	url   string
	title string
	loop  bool
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
	}
	bot.current = 0
	bot.yt_dlp = nil

	connect(vc)

	for bot.current < len(bot.queue) {

		bot.encodeChan = make(chan []int16, 450)
		bot.outputChan = make(chan []byte, 450)

		log.Printf("Current: %d ", bot.current)
		url := bot.queue[bot.current].url
		log.Printf("Current: %s", url)

		bot.downloader(url)
		go bot.reader()
		go bot.encoder()

		bot.play_sound(vc)

		bot.yt_dlp.Process.Signal(syscall.SIGINT)
		bot.ffmpeg.Process.Signal(syscall.SIGKILL)
		bot.yt_dlp.Wait()
		bot.ffmpeg.Wait()

		if !bot.loopOne {
			bot.current++
		}
		if bot.current >= len(bot.queue) && bot.loop {
			bot.current = 0
		}
	}

	bot.queue = bot.queue[:0]
	bot.current = 0

	disconnect(vc)
}

func (bot *Bot) downloader(url string) {
	bot.yt_dlp = exec.Command("yt-dlp", "-f", "251", "-o", "-", url)
	bot.ffmpeg = exec.Command("ffmpeg", "-i", "pipe:0", "-f", "s16le", "-acodec", "pcm_s16le", "pipe:1")
	bot.ffmpeg.Stdin, _ = bot.yt_dlp.StdoutPipe()
	bot.ffmpegStream, _ = bot.ffmpeg.StdoutPipe()

	// Start yt-dlp command
	if err := bot.yt_dlp.Start(); err != nil {
		log.Println("Error starting yt-dlp command:", err)
		return
	}

	// Start ffmpeg command
	if err := bot.ffmpeg.Start(); err != nil {
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

	bot.playing = true
	for bot.playing {
		opus, ok := <-bot.outputChan
		if !ok {
			bot.playing = false
			log.Println("End")
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
	bot.playing = false
	bot.loopOne = false
}

func (bot *Bot) Loop(mode int) string {
	if mode == 0 {
		bot.loop = !bot.loop
		return strconv.FormatBool(bot.loop)
	} else {
		bot.loopOne = !bot.loopOne
		return strconv.FormatBool(bot.loopOne) + " - " + bot.Current()
	}
}

func (bot *Bot) Play(url string, channelID string) {
	title := Title{url: url, title: url, loop: false}
	bot.queue = append(bot.queue, &title)

	go bot.getTitle(&title)

	if len(bot.queue) == 1 {
		bot.play(channelID)
	}
}

func (bot *Bot) Clear() {
	bot.queue = bot.queue[:0]
	bot.loop = false
	bot.loopOne = false
	bot.playing = false
}

func (bot *Bot) Queue() string {
	var titles string
	titles += "Loop: " + strconv.FormatBool(bot.loop) + "\n"
	for count, m := range bot.queue {
		if bot.current == count {
			titles += "  > "
			if bot.loopOne == true {
				titles += " loop "
			}
		}
		titles += strconv.Itoa(count) + ": " + m.title + "\n"
	}
	return titles
}

func (bot *Bot) Jump(num int) {
	bot.playing = false
	bot.current = num - 1
	log.Printf("Current: %d ", num-1)
}

func (bot *Bot) Current() string {
	return bot.queue[bot.current].url
}

func (bot *Bot) getTitle(title *Title) {
	cmd := exec.Command("yt-dlp", title.url, "--get-title")

	output, err := cmd.Output()
	if err != nil {
		log.Println("Error executing command:", err)
	}

	output_string := string(output)
	log.Printf("Title: %s", output_string)
	title.title = output_string
}
