//go:build ppc || ppc64

package adaptiveservice

var fakeInfo infoPerRoutine

func getRoutineLocal() *infoPerRoutine {
	return &fakeInfo
}

func goID() int64 {
	return 0
}
