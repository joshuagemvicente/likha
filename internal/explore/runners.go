package explore

// Named agent profiles extend the explore task tree without changing its
// budgets, records, or scheduler. Profile names come from the frozen run
// catalog: the task tool advertises exactly the names validateSpec admits,
// and the tools registry re-intersects the selected profile's permissions
// with the parent's effective capabilities at dispatch, so a Runner only
// ever sees a Node whose scope is already narrowed. runnerFor reads Config
// lock-free: New freezes the configuration and nothing mutates it afterward.
// A named key takes precedence over the legacy Runner; a missing key, an
// empty map, and the built-in "explore" name all fall back to it. A present
// but nil Runner is returned as nil so the execute path fails the task fast
// instead of panicking.
func (m *Manager) runnerFor(agent string) Runner {
	if runner, ok := m.config.Runners[agent]; ok {
		return runner
	}
	return m.config.Runner
}
