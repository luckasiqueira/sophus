package repo

import "errors"

var ErrInvalidAddonPrice = errors.New("os preços dos adicionais devem ser valores em centavos não negativos")

// Global monthly unit prices, shared by plans that allow add-ons.
type AddonPrices struct {
	ExtraConnectionPriceCents    int64 `json:"extraConnectionPriceCents"`
	ExtraAgentPriceCents         int64 `json:"extraAgentPriceCents"`
	ExtraStorageGBPriceCents     int64 `json:"extraStorageGBPriceCents"`
	AudioTranscriptionPriceCents int64 `json:"audioTranscriptionPriceCents"`
}

func (prices AddonPrices) Validate() error {
	for _, price := range []int64{prices.ExtraConnectionPriceCents, prices.ExtraAgentPriceCents, prices.ExtraStorageGBPriceCents, prices.AudioTranscriptionPriceCents} {
		if price < 0 || price > 9007199254740991 {
			return ErrInvalidAddonPrice
		}
	}
	return nil
}

func GetAddonPrices() (AddonPrices, error) {
	return scanAddonPrices(db.QueryRow(`SELECT "extraConnectionPriceCents", "extraAgentPriceCents",
 "extraStorageGBPriceCents", "audioTranscriptionPriceCents" FROM platform_settings WHERE id = 1`))
}

func UpdateAddonPrices(input AddonPrices) (AddonPrices, error) {
	if err := input.Validate(); err != nil {
		return AddonPrices{}, err
	}
	return scanAddonPrices(db.QueryRow(`UPDATE platform_settings SET "extraConnectionPriceCents" = $1,
 "extraAgentPriceCents" = $2, "extraStorageGBPriceCents" = $3, "audioTranscriptionPriceCents" = $4,
 "updatedAt" = now() WHERE id = 1 RETURNING "extraConnectionPriceCents", "extraAgentPriceCents",
 "extraStorageGBPriceCents", "audioTranscriptionPriceCents"`, input.ExtraConnectionPriceCents,
		input.ExtraAgentPriceCents, input.ExtraStorageGBPriceCents, input.AudioTranscriptionPriceCents))
}

func scanAddonPrices(scanner rowScanner) (AddonPrices, error) {
	var prices AddonPrices
	err := scanner.Scan(&prices.ExtraConnectionPriceCents, &prices.ExtraAgentPriceCents,
		&prices.ExtraStorageGBPriceCents, &prices.AudioTranscriptionPriceCents)
	return prices, err
}
