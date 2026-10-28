package main

import (
	"fmt"
	"slices"
)

// window keeps the lines that fit in room, the cursor's row among them,
// and says how many it leaves out above and below.
func window(lines []string, row, room int) []string {
	if len(lines) <= room {
		return lines
	}
	room = max(1, room-2)
	first := min(max(0, row-room/2), len(lines)-room)
	last := first + room
	more := func(count int, arrow string) string {
		if count == 0 {
			return ""
		}
		return faint.Render(fmt.Sprintf("   %s %d more", arrow, count))
	}
	return slices.Concat([]string{more(first, "↑")}, lines[first:last], []string{more(len(lines)-last, "↓")})
}
