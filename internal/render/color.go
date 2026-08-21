package render

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
)

func GlassSVGFill(nd, vd float64) string {
	r := (2.5-nd)/(2.5-1.4)*90 + (100-vd)/(100-20)*90
	g := (2.5-nd)/(2.5-1.4)*150 + (vd-20)/(100-20)*100
	b := (2.5-nd)/(2.5-1.4)*70 + 180
	return fmt.Sprintf("rgb(%.0f,%.0f,%.0f)", r, g, b)
}

func ColorToHex(c color.NRGBA) string {
	return fmt.Sprintf("rgb(%d,%d,%d)", c.R, c.G, c.B)
}

var namedColors = map[string]color.NRGBA{
	"red":       {255, 0, 0, 255},
	"green":     {0, 128, 0, 255},
	"blue":      {0, 0, 255, 255},
	"white":     {255, 255, 255, 255},
	"black":     {0, 0, 0, 255},
	"yellow":    {255, 255, 0, 255},
	"cyan":      {0, 255, 255, 255},
	"magenta":   {255, 0, 255, 255},
	"gray":      {128, 128, 128, 255},
	"grey":      {128, 128, 128, 255},
	"orange":    {255, 165, 0, 255},
	"purple":    {128, 0, 128, 255},
	"pink":      {255, 192, 203, 255},
	"brown":     {165, 42, 42, 255},
	"transparent": {0, 0, 0, 0},
}

func ParseColor(s string) (color.NRGBA, error) {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)

	if c, ok := namedColors[s]; ok {
		return c, nil
	}

	if strings.HasPrefix(s, "#") {
		hex := s[1:]
		switch len(hex) {
		case 3:
			r, _ := strconv.ParseUint(hex[0:1]+hex[0:1], 16, 8)
			g, _ := strconv.ParseUint(hex[1:2]+hex[1:2], 16, 8)
			b, _ := strconv.ParseUint(hex[2:3]+hex[2:3], 16, 8)
			return color.NRGBA{uint8(r), uint8(g), uint8(b), 255}, nil
		case 6:
			r, _ := strconv.ParseUint(hex[0:2], 16, 8)
			g, _ := strconv.ParseUint(hex[2:4], 16, 8)
			b, _ := strconv.ParseUint(hex[4:6], 16, 8)
			return color.NRGBA{uint8(r), uint8(g), uint8(b), 255}, nil
		case 8:
			r, _ := strconv.ParseUint(hex[0:2], 16, 8)
			g, _ := strconv.ParseUint(hex[2:4], 16, 8)
			b, _ := strconv.ParseUint(hex[4:6], 16, 8)
			a, _ := strconv.ParseUint(hex[6:8], 16, 8)
			return color.NRGBA{uint8(r), uint8(g), uint8(b), uint8(a)}, nil
		}
		return color.NRGBA{}, fmt.Errorf("invalid hex color: %s", s)
	}

	if strings.HasPrefix(s, "rgb(") && strings.HasSuffix(s, ")") {
		inner := strings.TrimSpace(s[4 : len(s)-1])
		parts := strings.Split(inner, ",")
		if len(parts) != 3 {
			return color.NRGBA{}, fmt.Errorf("invalid rgb color: %s", s)
		}
		r, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		g, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		b, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
		return color.NRGBA{uint8(r), uint8(g), uint8(b), 255}, nil
	}

	if strings.HasPrefix(s, "rgba(") && strings.HasSuffix(s, ")") {
		inner := strings.TrimSpace(s[5 : len(s)-1])
		parts := strings.Split(inner, ",")
		if len(parts) != 4 {
			return color.NRGBA{}, fmt.Errorf("invalid rgba color: %s", s)
		}
		r, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		g, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		b, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
		a, _ := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
		return color.NRGBA{uint8(r), uint8(g), uint8(b), uint8(a * 255)}, nil
	}

	return color.NRGBA{}, fmt.Errorf("unknown color format: %s", s)
}

func ParseColorMap(s string) (map[int]color.NRGBA, error) {
	result := make(map[int]color.NRGBA)
	if strings.TrimSpace(s) == "" {
		return result, nil
	}
	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eqIdx := strings.Index(pair, "=")
		if eqIdx < 0 {
			return nil, fmt.Errorf("invalid color map entry: %s (expected 'index=color')", pair)
		}
		keyStr := strings.TrimSpace(pair[:eqIdx])
		valStr := strings.TrimSpace(pair[eqIdx+1:])
		key, err := strconv.Atoi(keyStr)
		if err != nil {
			return nil, fmt.Errorf("invalid index in color map: %s", keyStr)
		}
		val, err := ParseColor(valStr)
		if err != nil {
			return nil, fmt.Errorf("invalid color for index %d: %v", key, err)
		}
		result[key] = val
	}
	return result, nil
}

func ParseAsphereColorMap(s string) (all color.NRGBA, byID map[int]color.NRGBA, err error) {
	byID = make(map[int]color.NRGBA)
	s = strings.TrimSpace(s)
	if s == "" {
		return color.NRGBA{}, byID, nil
	}

	if strings.Contains(s, "=") {
		byID, err = ParseColorMap(s)
		return color.NRGBA{}, byID, err
	}

	all, err = ParseColor(s)
	return all, byID, err
}
