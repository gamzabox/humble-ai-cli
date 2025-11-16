package app

type inputHistory struct {
	entries  []string
	browsing bool
	index    int
	pending  string
}

func newInputHistory() *inputHistory {
	return &inputHistory{
		entries: make([]string, 0, 32),
		index:   0,
	}
}

func (h *inputHistory) Add(entry string) {
	if entry == "" {
		h.Reset()
		return
	}
	h.entries = append(h.entries, entry)
	h.Reset()
}

func (h *inputHistory) Previous(current string) (string, bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	if !h.browsing {
		h.pending = current
		h.index = len(h.entries)
		h.browsing = true
	}
	if h.index == 0 {
		return "", false
	}
	h.index--
	return h.entries[h.index], true
}

func (h *inputHistory) Next() (string, bool) {
	if !h.browsing {
		return "", false
	}
	h.index++
	if h.index >= len(h.entries) {
		pending := h.pending
		h.Reset()
		return pending, true
	}
	return h.entries[h.index], true
}

func (h *inputHistory) Reset() {
	h.browsing = false
	h.index = len(h.entries)
	h.pending = ""
}
