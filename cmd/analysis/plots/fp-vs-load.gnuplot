set terminal pngcairo size 1200,800
set output "../../results/fp-vs-load.png"

set datafile separator ","
set key top left

set title "False Positive Rate vs. Load Factor"
set xlabel "Load Factor"
set ylabel "FP Rate"
set logscale y
set format y "%.0e"
set grid

plot "../../results/fp-vs-load.csv" \
    skip 1 using 1:2:3:4 with yerrorbars title "Observed (95% CI)" lc rgb "#3366cc", \
    "" skip 1 using 1:5 with lines title "Theoretical (blocked)" lc rgb "#cc3333" dt 2, \
    "" skip 1 using 1:6 with points title "Atomic" pt 4 ps 0.8 lc rgb "#33cc33", \
    "" skip 1 using 1:7 with points title "Sharded" pt 6 ps 0.8 lc rgb "#cc9933"
