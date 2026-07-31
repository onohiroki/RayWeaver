package glass

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/hiroki/rayweaver/internal/types"
)

var knownManufacturers = []string{"HOYA", "HIKARI", "OHARA", "SUMITA", "SCHOTT"}

// Warnf reports a non-fatal warning to stderr. The cmd/rayweave binary
// overrides it (via errOut) so warnings are attributed to the active
// subcommand in a pipeline.
var Warnf = func(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func ParseAGF(data []byte, sourceName ...string) ([]types.Glass, error) {
	data = decodeAGFContent(data)
	text := normalizeLineEndings(string(data))

	lines := strings.Split(text, "\n")

	srcName := ""
	if len(sourceName) > 0 {
		srcName = sourceName[0]
	}
	manufacturer := detectManufacturer(lines, srcName)

	var glasses []types.Glass
	var current *types.Glass

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "CC") || strings.HasPrefix(line, "CA") || strings.HasPrefix(line, "CU") {
			continue
		}

		if strings.HasPrefix(line, "CT") {
			continue
		}

		if strings.HasPrefix(line, "CD") {
			rest := strings.TrimSpace(line[2:])
			if current != nil && rest != "" {
				parts := strings.Fields(rest)
				for _, p := range parts {
					if v, err := strconv.ParseFloat(p, 64); err == nil {
						current.Coefficients = append(current.Coefficients, v)
					}
				}
				if len(current.Coefficients) > 0 {
					current.DispersionFormula = types.Sellmeier1
				}
			}
			continue
		}

		if strings.HasPrefix(line, "ED") {
			if current != nil {
				glasses = append(glasses, *current)
				current = nil
			}
			continue
		}

		if strings.HasPrefix(line, "NM") {
			if current != nil {
				glasses = append(glasses, *current)
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				name := fields[1]
				current = &types.Glass{
					Type:    types.GlassTypeCatalog,
					Key:     name,
					Name:    name,
					Aliases: []string{},
				}
				if len(fields) >= 6 {
					if nd, err := strconv.ParseFloat(fields[4], 64); err == nil {
						current.ND = nd
					}
				}
				if len(fields) >= 7 {
					if vd, err := strconv.ParseFloat(fields[5], 64); err == nil {
						current.VD = vd
					}
				}
			}
			continue
		}

		if strings.HasPrefix(line, "NAME") {
			if current != nil {
				glasses = append(glasses, *current)
			}
			name := strings.TrimSpace(line[4:])
			current = &types.Glass{
				Type:    types.GlassTypeCatalog,
				Key:     name,
				Name:    name,
				Aliases: []string{},
			}
			continue
		}

		if current == nil {
			continue
		}

		if strings.HasPrefix(line, "MANUFACTURER") {
			current.Manufacturer = strings.TrimSpace(line[12:])
			continue
		}
		if strings.HasPrefix(line, "MAN") {
			current.Manufacturer = strings.TrimSpace(line[3:])
			continue
		}

		if strings.HasPrefix(line, "ND") {
			val := strings.TrimSpace(line[2:])
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				current.ND = v
			}
			continue
		}

		if strings.HasPrefix(line, "VD") {
			val := strings.TrimSpace(line[2:])
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				current.VD = v
			}
			continue
		}

		if strings.HasPrefix(line, "CO") {
			parts := strings.Fields(line)
			for _, p := range parts {
				if v, err := strconv.ParseFloat(p, 64); err == nil {
					current.Coefficients = append(current.Coefficients, v)
				}
			}
			current.DispersionFormula = types.Sellmeier1
			continue
		}

		if strings.HasPrefix(line, "RANGE") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if v, err := strconv.ParseFloat(parts[0][5:], 64); err == nil {
					current.WavelengthMin = v
				}
				if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
					current.WavelengthMax = v
				}
			}
			continue
		}

		if strings.HasPrefix(line, "ALIAS") {
			alias := strings.TrimSpace(line[5:])
			if alias != "" {
				normalized := strings.ReplaceAll(alias, " ", "")
				current.Aliases = append(current.Aliases, normalized)
			}
			continue
		}
		if strings.HasPrefix(line, "GC") {
			alias := strings.TrimSpace(line[2:])
			if alias != "" {
				normalized := strings.ReplaceAll(alias, " ", "")
				current.Aliases = append(current.Aliases, normalized)
			}
			continue
		}
	}

	if current != nil {
		glasses = append(glasses, *current)
	}

	for i := range glasses {
		if glasses[i].Manufacturer == "" && manufacturer != "" {
			glasses[i].Manufacturer = manufacturer
		}
	}

	if err := bufio.NewScanner(strings.NewReader(text)).Err(); err != nil {
		return nil, fmt.Errorf("AGF scan error: %w", err)
	}

	return glasses, nil
}

func LoadAGFDir(dir string) ([]types.Glass, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot read AGF directory %s: %w", dir, err)
	}
	var allGlasses []types.Glass
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".agf") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			Warnf("Warning: cannot read AGF file %s: %v", path, err)
			continue
		}
		glasses, err := ParseAGF(data, filepath.Base(path))
		if err != nil {
			Warnf("Warning: cannot parse AGF file %s: %v", path, err)
			continue
		}
		allGlasses = append(allGlasses, glasses...)
	}
	return allGlasses, nil
}

func detectManufacturer(lines []string, sourceName string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "CC") {
			upper := strings.ToUpper(trimmed)
			for _, km := range knownManufacturers {
				if strings.Contains(upper, km) {
					return km
				}
			}
			rest := strings.TrimSpace(trimmed[2:])
			if rest != "" {
				tokens := strings.Fields(rest)
				if len(tokens) > 0 {
					return tokens[0]
				}
			}
		}
	}

	if sourceName != "" {
		upper := strings.ToUpper(sourceName)
		for _, km := range knownManufacturers {
			if strings.Contains(upper, km) {
				return km
			}
		}
		base := strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
		base = strings.TrimSuffix(base, ".AGF")
		base = strings.TrimSuffix(base, ".agf")
		tokens := strings.FieldsFunc(base, func(r rune) bool {
			return r == '_' || r == '-' || r == ' '
		})
		if len(tokens) > 0 {
			return strings.ToUpper(tokens[0])
		}
	}

	return ""
}

func decodeAGFContent(raw []byte) []byte {
	if len(raw) < 2 {
		return raw
	}
	if raw[0] == 0xFF && raw[1] == 0xFE {
		u16 := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
		return []byte(string(utf16.Decode(u16)))
	}
	if raw[0] == 0xFE && raw[1] == 0xFF {
		u16 := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			u16 = append(u16, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
		return []byte(string(utf16.Decode(u16)))
	}
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return raw[3:]
	}
	return raw
}

func normalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}
