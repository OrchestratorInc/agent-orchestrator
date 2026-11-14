package ports

import "errors"

// ErrComputerUseDriverNotFound is returned when Cua Driver is not installed
// in any location its official installers use. It lives here because the
// adapter that returns it cannot import service/computeruse, which checks it;
// the driver interface itself is defined there, its only consumer.
var ErrComputerUseDriverNotFound = errors.New("computer use: cua-driver not found")
