set terminal pngcairo size 1200,800
set output "../../results/fp-vs-k.png"

set datafile separator ","
set key top right

set title "False Positive Rate Across k Values"
set xlabel "k (hash functions)"
set ylabel "FP Rate"
set logscale y
set format y "%.0e"
set grid
set xrange [2:15]
set xtics 1

plot "../../results/fp-vs-k.csv" \
    skip 1 using 1:2:3:4 with yerrorbars title "Observed (95% CI)" lc rgb "#3366cc" pt 7 ps 1.2, \
    "" skip 1 using 1:5 with linespoints title "Theoretical (blocked)" lc rgb "#cc3333" dt 2 pt 5 ps 1.0
