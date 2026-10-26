package livetv

import "time"

// MatchChannel is the channel identity the sports matcher needs. Stream URLs
// and credentials stay on the server; clients play through the live session
// endpoint using the channel ID.
type MatchChannel struct {
	ID         string
	Name       string
	TvgName    string
	TvgID      string
	Group      string
	SourceID   string
	SourceName string
	Logo       string
	Metadata   string
	Now        *Program
	Next       *Program
}

// MatchChannels lists every visible channel with its EPG now/next. It is
// server-side only and deliberately not a JSON contract.
func (s *Service) MatchChannels() []MatchChannel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sourceNames := map[string]string{}
	for _, source := range s.cfg.LiveTV.Sources {
		sourceNames[source.ID] = source.Name
	}
	t := time.Now().Unix()
	out := make([]MatchChannel, 0, len(s.visible))
	for _, c := range s.visible {
		now, next := s.guide.NowNext(c.ID, t)
		name := sourceNames[c.Source]
		if name == "" {
			name = c.Source
		}
		out = append(out, MatchChannel{
			ID: c.ID, Name: c.Name, TvgName: c.TvgName, TvgID: c.TvgID,
			Group: groupName(c), SourceID: c.Source, SourceName: name, Logo: c.Logo,
			Metadata: c.Metadata,
			Now:      cloneProgram(now), Next: cloneProgram(next),
		})
	}
	return out
}

// MatchChannelByID resolves one channel for administrative previews.
func (s *Service) MatchChannelByID(id string) (MatchChannel, bool) {
	for _, channel := range s.MatchChannels() {
		if channel.ID == id {
			return channel, true
		}
	}
	return MatchChannel{}, false
}

func cloneProgram(p *Program) *Program {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}
