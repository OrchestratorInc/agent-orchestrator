//go:build windows

package local

import "os"

// stopStaleHub has nothing to stop on Windows, where device support runs no
// simulators; the record is cleared so it cannot go stale.
func (p *Provider) stopStaleHub(string) {
	_ = os.Remove(p.hubPIDPath())
}
