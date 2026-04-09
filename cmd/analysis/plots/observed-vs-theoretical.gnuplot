set terminal pngcairo size 1000,1000
set output "../../results/observed-vs-theoretical.png"

set datafile separator ","
set key bottom right

set title "Observed vs. Theoretical FP Rate"
set xlabel "Theoretical FP Rate"
set ylabel "Observed FP Rate"
set logscale x
set logscale y
set format x "%.0e"
set format y "%.0e"
set grid
set size square

set style line 1 lc rgb "#cccccc" dt 2 lw 2

plot "../../results/observed-vs-theoretical.csv" \
    skip 1 using 4:3 with points pt 7 ps 1.2 lc rgb "#3366cc" title "Data points", \
    x with lines ls 1 title "y = x"
