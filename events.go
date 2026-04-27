package piper

import "github.com/piper/piper/pkg/event"

func (p *Piper) emitEvent(eventType string, fields map[string]any) {
	if p.events == nil {
		return
	}
	p.events.Publish(event.New(eventType, fields))
}
