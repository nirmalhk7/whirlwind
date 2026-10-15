package settingsfile

import (
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (doc *Document) apply(change store.Change) error {
	data, err := json.Marshal(change.Value)
	if err != nil {
		return err
	}
	switch change.Kind {
	case "settings":
		err = json.Unmarshal(data, &doc.Workers)
	case "agents":
		var value model.AgentConfig
		if err = json.Unmarshal(data, &value); err == nil {
			doc.Agents[change.ID] = value.Chain
		}
	case "providers":
		var value model.Provider
		if err = json.Unmarshal(data, &value); err == nil {
			doc.setProvider(project(value))
		}
	}
	return err
}

func (doc *Document) setProvider(p Provider) {
	for i, old := range doc.Providers {
		if old.ID == p.ID {
			doc.Providers[i] = p
			return
		}
	}
	doc.Providers = append(doc.Providers, p)
}
