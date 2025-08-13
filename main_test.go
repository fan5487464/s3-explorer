package main

import (
	"testing"

	"s3-explorer/common"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
	}

	for _, test := range tests {
		result := common.FormatBytes(test.input)
		if result != test.expected {
			t.Errorf("FormatBytes(%d) = %s; expected %s", test.input, result, test.expected)
		}
	}
}

func TestGetIconForFile(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"image.png", "image"},
		{"photo.jpg", "image"},
		{"audio.mp3", "audio"},
		{"music.wav", "audio"},
		{"video.mp4", "video"},
		{"movie.avi", "video"},
		{"archive.zip", "archive"},
		{"compressed.tar.gz", "archive"},
		{"document.txt", "text"},
		{"readme.md", "text"},
		{"unknown.xyz", "file"},
	}

	for _, test := range tests {
		result := common.GetIconForFile(test.filename)
		if result != test.expected {
			t.Errorf("GetIconForFile(%s) = %s; expected %s", test.filename, result, test.expected)
		}
	}
}

func TestIsPreviewableImage(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"image.png", true},
		{"photo.jpg", true},
		{"picture.jpeg", true},
		{"animation.gif", true},
		{"graphic.bmp", false},
		{"vector.svg", false},
		{"document.pdf", false},
	}

	for _, test := range tests {
		result := common.IsPreviewableImage(test.filename)
		if result != test.expected {
			t.Errorf("IsPreviewableImage(%s) = %t; expected %t", test.filename, result, test.expected)
		}
	}
}

func TestFormatFileNameForDisplay(t *testing.T) {
	tests := []struct {
		filename          string
		maxDisplayLength  int
		expected          string
	}{
		{"short.txt", 20, "short.txt"},
		{"very_long_filename_that_exceeds_the_limit.txt", 20, "very_long_fil....txt"},
		{"exact_length_filename.txt", 25, "exact_length_filen....txt"},
		{"just_one_char_over.txt", 20, "just_one_char....txt"},
	}

	for _, test := range tests {
		result := common.FormatFileNameForDisplay(test.filename, test.maxDisplayLength)
		if result != test.expected {
			t.Errorf("FormatFileNameForDisplay(%s, %d) = %s; expected %s", test.filename, test.maxDisplayLength, result, test.expected)
		}
	}
}