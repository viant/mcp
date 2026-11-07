package manager

const visibleCatalogCapacity = 256

// Called with m.mu held. Do not interrupt a catalog, entry, or representation
// population. Existing publication and cursor fences reject evicted identities.
func (m *Manager) makeVisibleCatalogRoomLocked() bool {
	if len(m.visibleCatalogs) < visibleCatalogCapacity {
		return true
	}
	var oldest *visibleCatalog
	var victim string
	for key, slot := range m.visibleCatalogs {
		if slot.ready != nil {
			continue
		}
		busy := false
		for _, entry := range slot.entries {
			if entry.ready != nil {
				busy = true
				break
			}
			for _, representation := range entry.representations {
				if representation.ready != nil {
					busy = true
					break
				}
			}
			if busy {
				break
			}
		}
		if busy {
			continue
		}
		if oldest == nil || slot.lastUsed.Before(oldest.lastUsed) {
			oldest, victim = slot, key
		}
	}
	if oldest == nil {
		return false
	}
	delete(m.visibleCatalogs, victim)
	return true
}
