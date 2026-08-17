package presentation

import "strings"

type Tone int

const (
	ToneNormal Tone = iota
	ToneMuted
	ToneHealthy
	ToneWaiting
	ToneFailure
	ToneHeading
)

type Span struct {
	Text string
	Tone Tone
}

type Line []Span

type Frame struct {
	Lines []Line
}

func (f *Frame) Add(width int, spans ...Span) {
	if width <= 0 {
		return
	}
	remaining := width
	line := make(Line, 0, len(spans))
	for _, span := range spans {
		if remaining == 0 {
			break
		}
		runes := []rune(span.Text)
		if len(runes) > remaining {
			runes = runes[:remaining]
		}
		if len(runes) > 0 {
			line = append(line, Span{Text: string(runes), Tone: span.Tone})
			remaining -= len(runes)
		}
	}
	f.Lines = append(f.Lines, line)
}

func (f *Frame) Blank(width int) {
	f.Add(width, Span{})
}

func (f Frame) PlainLines() []string {
	lines := make([]string, len(f.Lines))
	for i, line := range f.Lines {
		var builder strings.Builder
		for _, span := range line {
			builder.WriteString(span.Text)
		}
		lines[i] = builder.String()
	}
	return lines
}

func (f Frame) FitHeight(height int, footer Line) Frame {
	if height <= 0 || len(f.Lines) <= height {
		return f
	}
	if height == 1 {
		return Frame{Lines: []Line{footer}}
	}
	lines := append([]Line(nil), f.Lines[:height-1]...)
	lines = append(lines, footer)
	return Frame{Lines: lines}
}
