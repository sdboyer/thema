package diag

import (
	"fmt"
	"runtime/debug"
)

// Internal describes a recovered panic as an [E0901] diagnostic, with the
// panic value and the stack as detail. Operation boundaries that must not
// crash their caller, such as the playground, use it:
//
//	defer func() {
//		if r := recover(); r != nil {
//			err = diag.Internal(r)
//		}
//	}()
func Internal(recovered any) *Error {
	return New(E0901).
		Detail(fmt.Sprintf("%v\n\n%s", recovered, debug.Stack())).
		Help("this is a bug in thema; please report it with the detail above").
		Err()
}
