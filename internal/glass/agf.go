package glass

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

func ParseAGF(data []byte) ([]types.Glass, error) {
	var glasses []types.Glass
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	var current *types.Glass
	inCatalog := false

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "CC") || strings.HasPrefix(line, "CA") || strings.HasPrefix(line, "CU") {
			continue
		}

		if strings.HasPrefix(line, "NM") && !inCatalog {
			continue
		}

		if strings.HasPrefix(line, "CD") {
			inCatalog = true
			continue
		}

		if strings.HasPrefix(line, "CT") {
			continue
		}

		if strings.HasPrefix(line, "ED") {
			if current != nil {
				glasses = append(glasses, *current)
				current = nil
			}
			inCatalog = false
			continue
		}

		if strings.HasPrefix(line, "NAME") {
			if current != nil {
				glasses = append(glasses, *current)
			}
			current = &types.Glass{
				Aliases: []string{},
			}
			current.Name = strings.TrimSpace(line[4:])
			continue
		}

		if current == nil {
			continue
		}

		if strings.HasPrefix(line, "MANUFACTURER") || strings.HasPrefix(line, "MAN") {
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

		if strings.HasPrefix(line, "CO") || strings.HasPrefix(line, "CD") {
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

		if strings.HasPrefix(line, "ALIAS") || strings.HasPrefix(line, "GC") {
			alias := strings.TrimSpace(line[4:])
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

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("AGF scan error: %w", err)
	}

	return glasses, nil
}
