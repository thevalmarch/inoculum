package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/inoculum/internal/presentation"
)

func TestDrawFrameOnSimulationScreenWithoutColor(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(40, 8)
	frame := presentation.Frame{}
	frame.Add(40, presentation.Span{Text: "INOCULUM", Tone: presentation.ToneHeading})
	frame.Add(40, presentation.Span{Text: "healthy", Tone: presentation.ToneHealthy})
	drawFrame(screen, frame, presentation.Capabilities{Color: false, Unicode: true})

	cells, width, _ := screen.GetContents()
	var firstLine strings.Builder
	for x := 0; x < width; x++ {
		if len(cells[x].Runes) > 0 {
			firstLine.WriteRune(cells[x].Runes[0])
		}
	}
	if !strings.Contains(firstLine.String(), "INOCULUM") {
		t.Fatalf("screen line = %q", firstLine.String())
	}
	foreground, _, _ := cells[width].Style.Decompose()
	if foreground != tcell.ColorDefault {
		t.Fatalf("no-color foreground = %v, want default", foreground)
	}
}

func TestRunWithScreenHandlesResizeAndQuit(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(80, 24)
	result := make(chan error, 1)
	go func() {
		result <- RunWithScreen(context.Background(), nil, screen, presentation.Capabilities{}, func(width, height int) presentation.Frame {
			frame := presentation.Frame{}
			frame.Add(width, presentation.Span{Text: "size"})
			return frame
		})
	}()

	screen.SetSize(40, 12)
	screen.PostEventWait(tcell.NewEventResize(40, 12))
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-result:
		if !errors.Is(err, ErrQuit) {
			t.Fatalf("RunWithScreen() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunWithScreen did not handle quit")
	}
}
