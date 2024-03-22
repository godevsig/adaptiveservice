//go:build ppc || ppc64

package adaptiveservice

func getRoutineLocal() *infoPerRoutine {
	return nil
}

func goID() int64 {
	return 0
}
