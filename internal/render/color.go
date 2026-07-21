package render

import "fmt"

func GlassSVGFill(nd, vd float64) string {
	r := (2.5-nd)/(2.5-1.4)*90 + (100-vd)/(100-20)*90
	g := (2.5-nd)/(2.5-1.4)*150 + (vd-20)/(100-20)*100
	b := (2.5-nd)/(2.5-1.4)*70 + 180
	return fmt.Sprintf("rgb(%.0f,%.0f,%.0f)", r, g, b)
}
