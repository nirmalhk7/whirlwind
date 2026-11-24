package control

import "errors"

const DispatchHealthOperation = "inspect.dispatch_health"
const DispatchHealthSnapshotKey = "dispatch_health"

var ErrDispatchHealthUnavailable = errors.New("dispatch health inspection unavailable")
