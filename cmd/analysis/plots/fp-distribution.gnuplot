set terminal pngcairo size 1200,800
set output "../../results/fp-distribution.png"

set datafile separator ","
set key top right

set title "Distribution of False Positive Counts (10K trials)"
set xlabel "False Positive Count"
set ylabel "Frequency"
set grid
set style fill solid 0.5

# Read metadata for Gaussian overlay
stats "../../results/fp-distribution-meta.csv" skip 1 using 1 nooutput name "P"
stats "../../results/fp-distribution-meta.csv" skip 1 using 2 nooutput name "N"

# Gaussian overlay using binomial variance: Var = np(1-p).
# This slightly underestimates the true variance because it ignores
# filter-to-filter variation from Poisson block fill patterns. The effect
# is ~1% for typical configurations (many blocks) and negligible visually.
mu = P_min * N_min
sigma = sqrt(mu * (1.0 - P_min))
scale = 10000.0 * 1.0

gauss(x) = scale / (sigma * sqrt(2*pi)) * exp(-0.5 * ((x - mu) / sigma)**2)

binwidth = 1
set boxwidth binwidth
bin(x, width) = width * floor(x / width) + width / 2.0

plot "../../results/fp-distribution.csv" \
    skip 1 using (bin($2, binwidth)):(1.0) smooth freq with boxes \
    lc rgb "#3366cc" title "Observed", \
    gauss(x) with lines lw 2 lc rgb "#cc3333" title "Theoretical N({/Symbol m}, {/Symbol s})"
