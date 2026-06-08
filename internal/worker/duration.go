package worker

import (
	"encoding/binary"
	"fmt"
	"os"
	"time"
)

func parseM4ADuration(filePath string) (time.Duration, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	fileSize := fi.Size()

	offset := int64(0)
	header := make([]byte, 16)

	for offset+8 <= fileSize {
		if _, err := f.ReadAt(header[:8], offset); err != nil {
			break
		}

		size := int64(binary.BigEndian.Uint32(header[0:4]))
		boxType := string(header[4:8])
		headerLen := int64(8)

		if size == 1 {
			if offset+16 > fileSize {
				break
			}
			if _, err := f.ReadAt(header[8:16], offset+8); err != nil {
				break
			}
			size = int64(binary.BigEndian.Uint64(header[8:16]))
			headerLen = 16
		}

		if size == 0 {
			size = fileSize - offset
		}
		if size < headerLen {
			break
		}

		if boxType == "moov" {
			d, ok := parseMoovChildren(f, offset+headerLen, offset+size)
			if ok {
				return d, nil
			}
		}

		offset += size
	}

	return 0, fmt.Errorf("moov box not found in %s", filePath)
}

func parseMoovChildren(f *os.File, start int64, end int64) (time.Duration, bool) {
	offset := start
	header := make([]byte, 16)

	for offset+8 <= end {
		if _, err := f.ReadAt(header[:8], offset); err != nil {
			break
		}

		size := int64(binary.BigEndian.Uint32(header[0:4]))
		boxType := string(header[4:8])
		headerLen := int64(8)

		if size == 1 {
			if offset+16 > end {
				break
			}
			if _, err := f.ReadAt(header[8:16], offset+8); err != nil {
				break
			}
			size = int64(binary.BigEndian.Uint64(header[8:16]))
			headerLen = 16
		}

		if size == 0 {
			size = end - offset
		}
		if size < headerLen {
			break
		}

		if boxType == "mvhd" {
			data := make([]byte, size-headerLen)
			if _, err := f.ReadAt(data, offset+headerLen); err != nil {
				break
			}
			return parseMvhd(data)
		}

		offset += size
	}

	return 0, false
}

func parseMvhd(data []byte) (time.Duration, bool) {
	if len(data) < 20 {
		return 0, false
	}

	version := data[0]
	var timescale, duration uint64

	switch version {
	case 0:
		if len(data) < 20 {
			return 0, false
		}
		timescale = uint64(binary.BigEndian.Uint32(data[12:16]))
		duration = uint64(binary.BigEndian.Uint32(data[16:20]))
	case 1:
		if len(data) < 28 {
			return 0, false
		}
		timescale = uint64(binary.BigEndian.Uint32(data[20:24]))
		duration = binary.BigEndian.Uint64(data[24:32])
	default:
		return 0, false
	}

	if timescale == 0 {
		return 0, false
	}

	seconds := float64(duration) / float64(timescale)
	return time.Duration(seconds * float64(time.Second)), true
}

func formatDuration(d time.Duration) string {
	total := d.Round(time.Second)
	h := total / time.Hour
	total -= h * time.Hour
	m := total / time.Minute
	total -= m * time.Minute
	s := total / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
