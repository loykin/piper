package piper

import "github.com/piper/piper/internal/event"

func (p *Piper) emitEvent(projectID, eventType string, fields map[string]any) {
	if p.events == nil {
		return
	}
	if projectID == "" {
		p.events.Publish(event.NewInfra(eventType, fields))
	} else {
		p.events.Publish(event.New(projectID, eventType, fields))
	}
}
