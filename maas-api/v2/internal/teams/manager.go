package teams

// Manager is a minimal wrapper for backward compatibility
// All team operations are now handled directly by the database repository
type Manager struct {
	// Keeping this struct minimal since all functionality moved to repository
}

// NewManager creates a new team manager (minimal for compatibility)
func NewManager() *Manager {
	return &Manager{}
}
