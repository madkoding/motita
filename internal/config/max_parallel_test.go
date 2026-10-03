package config

import "testing"

// Three background agents by default, and the environment can change it - zero included, which
// turns spawn_agent off.
func TestMaxParallelDefaultAndEnvironment(t *testing.T) {
	if got := Default().Agent.MaxParallel; got != 3 {
		t.Errorf("max_parallel = %d, want 3", got)
	}
	t.Setenv("MOTITA_AGENT_MAX_PARALLEL", "0")
	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatal(err)
	}
	if c.Agent.MaxParallel != 0 {
		t.Errorf("MOTITA_AGENT_MAX_PARALLEL=0 -> %d", c.Agent.MaxParallel)
	}
}
