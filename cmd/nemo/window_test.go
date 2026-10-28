package main

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func TestWindow(t *testing.T) {
	t.Parallel()
	lines := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	assert.Equal(t, lines[:3], window(lines[:3], 0, 5), "it fits")
	shown := window(lines, 9, 5)
	assert.Equal(t, []string{"7", "8", "9"}, shown[1:4], "the last ones, with the cursor")
	assert.Contains(t, ansi.Strip(shown[0]), "↑ 7 more")
	assert.Empty(t, shown[4], "nothing below")
	assert.Equal(t, []string{"4", "5", "6"}, window(lines, 5, 5)[1:4], "the cursor in the middle")
}
