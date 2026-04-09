set terminal pngcairo size 1200,800
set output "../../results/fp-vs-size.png"

set datafile separator ","
set key top left

set title "False Positive Rate vs. Filter Size"
set xlabel "Expected Items"
set ylabel "FP Rate"
set logscale x
set logscale y
set format y "%.0e"
set format x "%.0e"
set grid

plot "../../results/fp-vs-size.csv" \
    skip 1 using 1:2:3:4 with yerrorbars title "Observed (95% CI)" lc rgb "#3366cc", \
    "" skip 1 using 1:5 with lines title "Theoretical (blocked)" lc rgb "#cc3333" dt 2, \
    "" skip 1 using 1:6 with lines title "Target (1%)" lc rgb "#999999" dt 4
