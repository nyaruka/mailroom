package public

import "time"

var LockAndLoadContact = lockAndLoadContact

// SetContactLockWait overrides how long IVR requests wait for the contact lock, returning a function to restore it
func SetContactLockWait(d time.Duration) func() {
	prev := contactLockWait
	contactLockWait = d
	return func() { contactLockWait = prev }
}
