set terminal pngcairo size 1200,800 enhanced font "Helvetica,12"
set output "GLASS/glass_scatter.png"

set title "Glass nd-vd Distribution (2045 entries, 5 catalogs)"
set xlabel "nd (refractive index at 587.6 nm)" offset 0,1
set ylabel "vd (Abbe number)" offset 2,0

set xrange [1.35:2.25]
set yrange [10:110]

set grid xtics ytics lc rgb "#d0d0d0" lt 1
set border lc rgb "#404040"

# Glass line: vd = 10 * nd / (nd - 1)
set arrow from 1.35, 10*1.35/(1.35-1) to 2.25, 10*2.25/(2.25-1) nohead lc rgb "#ff6666" dt 2 lw 2 front

set key outside right top title "Manufacturer" box 3

# Manufacturer colors (up to 6)
HOYA    = "#1f77b4"
OHARA   = "#ff7f0e"
SCHOTT  = "#2ca02c"
SUMITA  = "#d62728"
HIKARI  = "#9467bd"

plot "/var/folders/3s/g6rl3qwj3nv77y50_97t_xnc0000gn/T/opencode/glass_scatter.dat" \
      using 1:2:(strcol(4) eq "HOYA"   ? 1 : 1/0) with points pt 7 ps 0.8 lc rgb HOYA   title "HOYA", \
   "" using 1:2:(strcol(4) eq "OHARA"  ? 1 : 1/0) with points pt 7 ps 0.8 lc rgb OHARA  title "OHARA", \
   "" using 1:2:(strcol(4) eq "SCHOTT" ? 1 : 1/0) with points pt 7 ps 0.8 lc rgb SCHOTT title "SCHOTT", \
   "" using 1:2:(strcol(4) eq "SUMITA" ? 1 : 1/0) with points pt 7 ps 0.8 lc rgb SUMITA title "SUMITA", \
   "" using 1:2:(strcol(4) eq "Updated" ? 1 : 1/0) with points pt 7 ps 0.8 lc rgb HIKARI title "HIKARI/NIKON"
