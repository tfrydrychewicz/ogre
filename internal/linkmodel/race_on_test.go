//go:build race

package linkmodel

// raceEnabled says the tests run under the race detector, which makes
// everything several times slower; tests with a ceiling on time allow for it.
const raceEnabled = true
