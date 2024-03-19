//go:build !ppc && !ppc64

package adaptiveservice

import "github.com/timandy/routine"

var routineLocal = routine.NewThreadLocalWithInitial(func() any {
	return &infoPerRoutine{}
})

func getRoutineLocal() *infoPerRoutine {
	return routineLocal.Get().(*infoPerRoutine)
}

func goID() int64 {
	return routine.Goid()
}
