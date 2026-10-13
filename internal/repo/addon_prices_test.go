package repo

import (
	"errors"
	"testing"
)

func TestAddonPricesValidate(t *testing.T) {
	for _, input := range []AddonPrices{{}, {ExtraConnectionPriceCents: 2500, ExtraAgentPriceCents: 1000, ExtraStorageGBPriceCents: 299, AudioTranscriptionPriceCents: 1490}} {
		if err := input.Validate(); err != nil {
			t.Fatalf("valid prices rejected: %v", err)
		}
	}
	for _, input := range []AddonPrices{
		{ExtraConnectionPriceCents: -1}, {ExtraAgentPriceCents: -1},
		{ExtraStorageGBPriceCents: -1}, {AudioTranscriptionPriceCents: -1},
		{ExtraConnectionPriceCents: 9007199254740992},
	} {
		// Rejection must happen before opening a database connection.
		if _, err := UpdateAddonPrices(input); !errors.Is(err, ErrInvalidAddonPrice) {
			t.Fatalf("invalid prices accepted: %+v, %v", input, err)
		}
	}
}
