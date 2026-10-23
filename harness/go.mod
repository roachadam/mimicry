module github.com/roachadam/mimicry/harness

go 1.27.3

require github.com/roachadam/mimicry v0.0.0-00010101000000-000000000000

// The harness is never imported by others, so this replace only affects it.
replace github.com/roachadam/mimicry => ../
