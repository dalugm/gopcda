package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	opcda "github.com/dalugm/gopcda"
)

type propertyOutput struct {
	ID       uint32 `json:"id"`
	Name     string `json:"name"`
	DataType string `json:"dataType"`
	Value    any    `json:"value"`
	Error    string `json:"error,omitempty"`
}

func printProperties(out io.Writer, id string, properties []opcda.ItemProperty) error {
	result := struct {
		ItemID            string           `json:"itemId"`
		DescriptionStatus string           `json:"descriptionStatus"`
		Properties        []propertyOutput `json:"properties"`
	}{ItemID: id, DescriptionStatus: "notProvided", Properties: make([]propertyOutput, 0, len(properties))}
	var failures []error
	for _, p := range properties {
		row := propertyOutput{
			ID:       p.ID,
			Name:     p.Name,
			DataType: fmt.Sprintf("0x%04X", p.DataType),
			Value:    p.Value,
		}
		if p.Error != nil {
			row.Value = nil
			row.Error = p.Error.Error()
			failures = append(failures, p.Error)
		} else if _, err := json.Marshal(row.Value); err != nil {
			// A non-finite value must not hide successfully read metadata.
			err = fmt.Errorf("property %d: encode value: %w", p.ID, err)
			row.Value = nil
			row.Error = err.Error()
			failures = append(failures, err)
		}
		if p.ID == opcda.PropertyItemDescription {
			switch value, ok := p.Value.(string); {
			case p.Error != nil:
				result.DescriptionStatus = "error"
			case !ok:
				result.DescriptionStatus = "error"
				row.Error = "item description is not a string"
				failures = append(failures, errors.New(row.Error))
			case value == "":
				result.DescriptionStatus = "empty"
			default:
				result.DescriptionStatus = "available"
			}
		}
		result.Properties = append(result.Properties, row)
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	return errors.Join(failures...)
}
