package custom

import "github.com/openai/openai-cli/internal/jsonview"

// The explorer checks Err after successful preload reads. An observed Agents
// outcome must not stop those reads before the iterator reaches EOF or a limit.
// The caller also joins the original iterator's cached error after local exit.
type agentsExplorerStream struct {
	source  jsonview.Iterator[outputJSON]
	stopped bool
}

func (s *agentsExplorerStream) Next() bool {
	s.stopped = !s.source.Next()
	return !s.stopped
}

func (s *agentsExplorerStream) Current() outputJSON { return s.source.Current() }

func (s *agentsExplorerStream) Err() error {
	if !s.stopped {
		return nil
	}
	return s.source.Err()
}
