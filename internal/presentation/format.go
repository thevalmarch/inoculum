package presentation

import (
	"fmt"
	"strings"
	"time"
)

func CompactDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	// Display completed whole seconds so live elapsed-time labels remain calm.
	// Runtime timing retains its original precision; only presentation is
	// truncated. Sub-second values therefore render as 0s.
	duration = duration.Truncate(time.Second)
	if duration < time.Minute {
		return fmt.Sprintf("%ds", duration/time.Second)
	}
	if duration < time.Hour {
		minutes := duration / time.Minute
		seconds := duration % time.Minute / time.Second
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return duration.Round(time.Minute).String()
}

func ShortID(id string, width int, unicode bool) string {
	if width <= 0 || len([]rune(id)) <= width {
		return id
	}
	prefix := "..."
	if unicode {
		prefix = "…"
	}
	runes := []rune(id)
	keep := width - len([]rune(prefix))
	if keep <= 0 {
		return string([]rune(prefix)[:width])
	}
	return prefix + string(runes[len(runes)-keep:])
}

func ProgressBar(done, total, width int, unicode bool) string {
	if width < 4 {
		return ""
	}
	if total <= 0 {
		total = 1
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	filled := done * width / total
	full, empty := "#", "-"
	if unicode {
		full, empty = "█", "░"
	}
	return strings.Repeat(full, filled) + strings.Repeat(empty, width-filled)
}

func PadBetween(left, right string, width int) string {
	space := width - len([]rune(left)) - len([]rune(right))
	if space < 1 {
		space = 1
	}
	return left + strings.Repeat(" ", space) + right
}

func SeenAgo(now, then time.Time) string {
	if then.IsZero() {
		return "never"
	}
	age := now.Sub(then)
	if age < 2*time.Second {
		return "now"
	}
	return fmt.Sprintf("%s ago", CompactDuration(age))
}

func Truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) > width {
		runes = runes[:width]
	}
	return string(runes)
}

func Wrap(text string, width int) []string {
	if width <= 0 || text == "" {
		return nil
	}
	runes := []rune(text)
	lines := make([]string, 0, (len(runes)+width-1)/width)
	for len(runes) > 0 {
		count := min(width, len(runes))
		lines = append(lines, string(runes[:count]))
		runes = runes[count:]
	}
	return lines
}
