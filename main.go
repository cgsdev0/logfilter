package main

// An example program demonstrating the pager component from the Bubbles
// component library.

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cgsdev0/logfilter/logview"
	tea "github.com/charmbracelet/bubbletea"
)

type Sink func(string)

func (sink Sink) tailStdin() error {
	sc := bufio.NewScanner(os.Stdin)
	defer os.Stdin.Close()
	for {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return err
			} else {
				sink("EOF\n")
			}
			break
		}
		sink(sc.Text() + "\n")
	}
	return nil
}

func (sink Sink) tailFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		bs, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		sink(string(bs))
		time.Sleep(time.Millisecond * 32)
	}
}

func main() {
	flag.Parse()

	program := tea.NewProgram(newScroll(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion())

	filename := flag.Arg(0)

	sinkErr := make(chan error)
	sink := Sink(func(s string) { program.Send(writeMsg(s)) })
	go func() {
		switch filename {
		case "-", "":
			sinkErr <- sink.tailStdin()
		default:
			sinkErr <- sink.tailFile(filename)
		}
	}()

	programErr := make(chan error)
	go func() {
		_, err := program.Run()
		programErr <- err
	}()

	select {
	case err := <-sinkErr:
		program.Send(tea.Quit())
		<-programErr
		fmt.Println(err)
		os.Exit(1)
	case err := <-programErr:
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	os.Exit(0)
}

type (
	writeMsg string
)

type scroll struct {
	logview *logview.Model
}

func newScroll() *scroll {
	return &scroll{logview.WithSoftWrap(logview.New())}
}

var _ tea.Model = &scroll{}

func (t *scroll) Init() tea.Cmd { return t.logview.Init() }

func (t *scroll) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.logview.SetDimensions(msg.Width, msg.Height)
		return t, nil
	case writeMsg:
		t.logview.Write(string(msg))
		return t, nil
	}
	model, cmd := t.logview.Update(msg)
	t.logview = model.(*logview.Model)
	return t, cmd
}

func (t *scroll) View() string { return t.logview.View() }
