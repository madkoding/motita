package onboard

import (
	_ "embed"
	"encoding/json"
)

// prices.json holds the approximate USD price per million tokens of the catalogue's models, as
// the vendors' own pricing pages list them, and the day they were last compared with those pages.
// Revise the file by hand, never from a model's memory: a model without an entry has no price.
//
//go:embed prices.json
var pricesJSON []byte

type price struct {
	In  float64 `json:"in"`
	Out float64 `json:"out"`
}

type priceTable struct {
	Checked string                      `json:"checked"`
	Prices  map[string]map[string]price `json:"prices"`
}

// catalogPrices is prices.json, read once. The file is part of the binary, so a mistake in it
// shows on every start and in the tests, never in the field.
var catalogPrices = readPrices(pricesJSON)

func readPrices(raw []byte) priceTable {
	var t priceTable
	if err := json.Unmarshal(raw, &t); err != nil {
		panic("onboard: prices.json: " + err.Error())
	}
	return t
}

// PricesChecked is the day the prices were last compared with the vendors' pricing pages.
func PricesChecked() string { return catalogPrices.Checked }
