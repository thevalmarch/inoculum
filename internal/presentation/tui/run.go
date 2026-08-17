package tui

import (
	"context"
	"errors"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/inoculum/internal/presentation"
)

var ErrQuit = errors.New("terminal UI requested shutdown")

type FrameSource func(width, height int) presentation.Frame

func Run(ctx context.Context, done <-chan struct{}, caps presentation.Capabilities, source FrameSource) error {
	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := screen.Init(); err != nil {
		return err
	}
	return RunWithScreen(ctx, done, screen, caps, source)
}

// RunWithScreen is exported so the terminal driver can be exercised with
// tcell's simulation screen rather than fragile ANSI byte assertions.
func RunWithScreen(ctx context.Context, done <-chan struct{}, screen tcell.Screen, caps presentation.Capabilities, source FrameSource) error {
	defer screen.Fini()
	screen.HideCursor()

	events := make(chan tcell.Event, 8)
	go func() {
		for {
			event := screen.PollEvent()
			if event == nil {
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	draw := func(help bool) {
		width, height := screen.Size()
		frame := source(width, height)
		if help {
			frame = helpFrame(width, height)
		}
		drawFrame(screen, frame, caps)
	}

	help := false
	draw(help)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			screen.PostEventWait(tcell.NewEventInterrupt(nil))
			return nil
		case <-done:
			screen.PostEventWait(tcell.NewEventInterrupt(nil))
			return nil
		case <-ticker.C:
			draw(help)
		case event := <-events:
			switch event := event.(type) {
			case *tcell.EventResize:
				screen.Sync()
				draw(help)
			case *tcell.EventKey:
				switch {
				case event.Key() == tcell.KeyCtrlC:
					return ErrQuit
				case event.Key() == tcell.KeyRune && (event.Rune() == 'q' || event.Rune() == 'Q'):
					return ErrQuit
				case event.Key() == tcell.KeyRune && event.Rune() == '?':
					help = !help
					draw(help)
				case event.Key() == tcell.KeyEscape && help:
					help = false
					draw(help)
				}
			}
		}
	}
}

func drawFrame(screen tcell.Screen, frame presentation.Frame, caps presentation.Capabilities) {
	width, height := screen.Size()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			screen.SetContent(x, y, ' ', nil, tcell.StyleDefault)
		}
	}

	for y, line := range frame.Lines {
		if y >= height {
			break
		}
		x := 0
		for _, span := range line {
			style := styleFor(span.Tone, caps.Color)
			for _, runeValue := range span.Text {
				if x >= width {
					break
				}
				screen.SetContent(x, y, runeValue, nil, style)
				x++
			}
		}
	}
	screen.Show()
}

func styleFor(tone presentation.Tone, color bool) tcell.Style {
	style := tcell.StyleDefault
	if tone == presentation.ToneHeading {
		style = style.Bold(true)
	}
	if !color {
		return style
	}
	switch tone {
	case presentation.ToneMuted:
		return style.Foreground(tcell.ColorGray)
	case presentation.ToneHealthy:
		return style.Foreground(tcell.ColorGreen)
	case presentation.ToneWaiting:
		return style.Foreground(tcell.ColorYellow)
	case presentation.ToneFailure:
		return style.Foreground(tcell.ColorRed)
	case presentation.ToneHeading:
		return style.Foreground(tcell.ColorWhite)
	default:
		return style
	}
}

func helpFrame(width, height int) presentation.Frame {
	frame := presentation.Frame{}
	frame.Add(width, presentation.Span{Text: "INOCULUM HELP", Tone: presentation.ToneHeading})
	frame.Blank(width)
	frame.Add(width, presentation.Span{Text: "q        quit"})
	frame.Add(width, presentation.Span{Text: "Ctrl+C   quit"})
	frame.Add(width, presentation.Span{Text: "? / Esc  close help"})
	frame.Blank(width)
	frame.Add(width, presentation.Span{Text: "This view is read-only.", Tone: presentation.ToneMuted})
	return frame.FitHeight(height, nil)
}
