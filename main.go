package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"github.com/layeh/gopus"
)

// All global variables used within the program
var (
	Channels  int
	FrameRate int

	FrameSize int = 960                 // uint16 size of each audio frame
	MaxBytes  int = (FrameSize * 2) * 2 // max size of opus data

	OpusEncoder *gopus.Encoder

	InFile string

	OutFile string = "pipe:1"
	OutBuf  []byte

	EncodeChan chan []int16
	OutputChan chan []byte

	err error

	wg sync.WaitGroup
)

// init configures and parses the command line arguments
func init() {

	flag.StringVar(&InFile, "i", "", "infile")
	flag.IntVar(&Channels, "ac", 2, "audio channels")
	flag.IntVar(&FrameRate, "ar", 48000, "audio sampling rate")
	flag.Parse()
}

// very simple program that wraps ffmpeg and outputs raw opus data frames
// with a uint16 header for each frame with the frame length in bytes
func main() {

	//////////////////////////////////////////////////////////////////////////
	// BLOCK : Basic setup and validation
	//////////////////////////////////////////////////////////////////////////

	// figure out if we're reading in from a file or stdin.
	fi, err := os.Stdin.Stat()
	if err != nil {
		fmt.Println(err)
		return
	}
	if (fi.Mode() & os.ModeCharDevice) == 0 {
		InFile = "pipe:0"
	} else {

		if InFile == "" {
			// TODO: possibly remove, here for legacy support
			if len(os.Args) < 2 {
				fmt.Println("Must supply the filename to process.")
				return
			}
			InFile = os.Args[1]
		}

		// make sure infile exists
		if _, err := os.Stat(InFile); os.IsNotExist(err) {
			fmt.Println("infile does not exist")
			return
		}
	}

	//////////////////////////////////////////////////////////////////////////
	// BLOCK : Create chans, buffers, and encoder for use
	//////////////////////////////////////////////////////////////////////////

	// create an opusEncoder to use
	OpusEncoder, err = gopus.NewEncoder(FrameRate, Channels, gopus.Audio)
	if err != nil {
		fmt.Println("NewEncoder Error:", err)
		return
	}

	OutputChan = make(chan []byte, 1)
	EncodeChan = make(chan []int16, 1)

	//////////////////////////////////////////////////////////////////////////
	// BLOCK : Start reader and writer workers
	//////////////////////////////////////////////////////////////////////////

	wg.Add(1)
	go reader()

	wg.Add(1)
	go encoder()

	wg.Add(1)
	go writer()

	// wait for above goroutines to finish, then exit.
	wg.Wait()
}

// reader reads from the input
func reader() {

	defer func() {
		close(EncodeChan)
		wg.Done()
	}()

	InBuf := make([]int16, FrameSize*Channels)

	// read from file
	if InFile != "pipe:0" {

		// Create a shell command "object" to run.
		ffmpeg := exec.Command("ffmpeg", "-i", InFile, "-f", "s16le", "-ar", strconv.Itoa(FrameRate), "-ac", strconv.Itoa(Channels), "pipe:1")
		stdout, err := ffmpeg.StdoutPipe()
		if err != nil {
			fmt.Println("StdoutPipe Error:", err)
			return
		}

		// Starts the ffmpeg command
		err = ffmpeg.Start()
		if err != nil {
			fmt.Println("RunStart Error:", err)
			return
		}

		for {

			// read data from ffmpeg stdout
			err = binary.Read(stdout, binary.LittleEndian, &InBuf)
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return
			}
			if err != nil {
				fmt.Println("error reading from ffmpeg stdout :", err)
				return
			}

			// write pcm data to the EncodeChan
			EncodeChan <- InBuf

		}
	}

	// read input from stdin pipe
	if InFile == "pipe:0" {
		for {

			// read data from stdin
			err = binary.Read(os.Stdin, binary.LittleEndian, &InBuf)
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return
			}
			if err != nil {
				fmt.Println("error reading from ffmpeg stdout :", err)
				return
			}

			// write pcm data to the EncodeChan
			EncodeChan <- InBuf
		}
	}

}

// encoder listens on the EncodeChan and encodes provided PCM16 data
// to opus, then sends the encoded data to the OutputChan
func encoder() {

	defer func() {
		close(OutputChan)
		wg.Done()
	}()

	for {
		pcm, ok := <-EncodeChan
		if !ok {
			// if chan closed, exit
			return
		}

		// try encoding pcm frame with Opus
		opus, err := OpusEncoder.Encode(pcm, FrameSize, MaxBytes)
		if err != nil {
			fmt.Println("Encoding Error:", err)
			return
		}

		// write opus data to OutputChan
		OutputChan <- opus
	}
}

// writer listens on the OutputChan and writes the output to stdout pipe
// TODO: Add support for writing directly to a file
func writer() {

	defer wg.Done()

	var opuslen uint16

	for {
		opus, ok := <-OutputChan
		if !ok {
			// if chan closed, exit
			return
		}

		// write header
		opuslen = uint16(len(opus))
		err = binary.Write(os.Stdout, binary.LittleEndian, &opuslen)
		if err != nil {
			fmt.Println("error writing output: ", err)
			return
		}

		// write opus data to stdout
		err = binary.Write(os.Stdout, binary.LittleEndian, &opus)
		if err != nil {
			fmt.Println("error writing output: ", err)
			return
		}
	}
}
