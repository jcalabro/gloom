set terminal pngcairo size 1200,800
set output "../../results/fp-vs-bpk.png"

set datafile separator ","
set key top right

set title "False Positive Rate vs. Bits per Key"
set xlabel "Bits per Key"
set ylabel "FP Rate"
set logscale y
set format y "%.0e"
set grid

plot "../../results/fp-vs-bpk.csv" \
    skip 1 using 1:4:5:6 with yerrorbars title "Observed (95% CI)" lc rgb "#3366cc", \
    "" skip 1 using 1:7 with lines title "Theoretical (blocked)" lc rgb "#cc3333" dt 2, \
    "" skip 1 using 1:8 with lines title "Standard (non-blocked)" lc rgb "#999999" dt 4
