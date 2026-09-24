// Package powerui controls the manual charge limit built into macOS through
// PowerUIAgent, the daemon behind System Settings -> Battery -> Charge Limit.
//
// On macOS 27 beta 4+ firmware the SMC charge-control keys can only be used by
// processes holding an Apple-private entitlement. PowerUIAgent holds it and
// exposes the limit over an XPC service that regular root processes may use,
// so batt delegates to it. Apple decides when charging starts and stops and
// only offers a fixed set of limits (80-100% at the time of writing).
package powerui

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#include "powerui.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"unsafe"

	"github.com/sirupsen/logrus"
)

// Controller is the subset of PowerUIAgent's smart-charge interface batt uses.
type Controller interface {
	// Supported reports whether the manual charge limit can be driven on this
	// Mac. It is false when PowerUI is missing or reports no MCL support.
	Supported() bool
	// AvailableLimits returns the upper limits PowerUIAgent accepts, ascending.
	AvailableLimits() ([]int, error)
	// Limit returns the configured limit and whether it is currently enforced.
	// A disabled limit reads as 100.
	Limit() (limit int, enabled bool, err error)
	// SetLimit enables the limit at the given percentage, which must be one of
	// AvailableLimits. Setting 100 disables the limit.
	SetLimit(limit int) error
	// Disable turns the limit off. PowerUIAgent remembers the last limit.
	Disable() error
}

// ErrUnavailable is returned when PowerUI cannot be used on this Mac.
var ErrUnavailable = errors.New("PowerUI manual charge limit is unavailable")

// Client talks to PowerUIAgent. It is safe for concurrent use.
type Client struct {
	mu sync.Mutex
}

// New returns a Client. Nothing is probed until the first call.
func New() *Client {
	return &Client{}
}

func takeError(cerr *C.char) error {
	if cerr == nil {
		return errors.New("unknown PowerUI error")
	}
	defer C.free(unsafe.Pointer(cerr))
	return errors.New(C.GoString(cerr))
}

func (c *Client) Supported() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	supported := C.batt_powerui_supported() == 1
	logrus.WithField("supported", supported).Trace("probed PowerUI manual charge limit")
	return supported
}

func (c *Client) AvailableLimits() ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	const maxLimits = 32
	var (
		raw   [maxLimits]C.int
		count C.int
		cerr  *C.char
	)
	if C.batt_powerui_available_limits(&raw[0], maxLimits, &count, &cerr) != 0 { //nolint:gocritic // dupSubExpr false positive on cgo call
		return nil, fmt.Errorf("get available charge limits: %w", takeError(cerr))
	}
	limits := make([]int, 0, int(count))
	for i := 0; i < int(count); i++ {
		limits = append(limits, int(raw[i]))
	}
	sort.Ints(limits)
	return limits, nil
}

func (c *Client) Limit() (int, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var (
		limit, enabled C.int
		cerr           *C.char
	)
	if C.batt_powerui_get_limit(&limit, &enabled, &cerr) != 0 { //nolint:gocritic // dupSubExpr false positive on cgo call
		return 0, false, fmt.Errorf("get native charge limit: %w", takeError(cerr))
	}
	logrus.WithFields(logrus.Fields{"limit": int(limit), "enabled": enabled == 1}).Trace("read native charge limit")
	return int(limit), enabled == 1, nil
}

func (c *Client) SetLimit(limit int) error {
	if limit < 0 || limit > 100 {
		return fmt.Errorf("invalid native charge limit %d", limit)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	var cerr *C.char
	if C.batt_powerui_set_limit(C.int(limit), &cerr) != 0 { //nolint:gocritic // dupSubExpr false positive on cgo call
		return fmt.Errorf("set native charge limit to %d%%: %w", limit, takeError(cerr))
	}
	logrus.WithField("limit", limit).Trace("set native charge limit")
	return nil
}

func (c *Client) Disable() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var cerr *C.char
	if C.batt_powerui_disable(&cerr) != 0 { //nolint:gocritic // dupSubExpr false positive on cgo call
		return fmt.Errorf("disable native charge limit: %w", takeError(cerr))
	}
	logrus.Trace("disabled native charge limit")
	return nil
}
