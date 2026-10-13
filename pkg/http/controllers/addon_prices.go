package controllers

import (
	"errors"
	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
)

func UpdateSaaSAddonPrices(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	// Require all four prices so partial requests cannot silently clear prices.
	var body struct {
		ExtraConnectionPriceCents    *int64 `json:"extraConnectionPriceCents"`
		ExtraAgentPriceCents         *int64 `json:"extraAgentPriceCents"`
		ExtraStorageGBPriceCents     *int64 `json:"extraStorageGBPriceCents"`
		AudioTranscriptionPriceCents *int64 `json:"audioTranscriptionPriceCents"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido; informe os valores em centavos inteiros")
		return
	}
	if body.ExtraConnectionPriceCents == nil || body.ExtraAgentPriceCents == nil || body.ExtraStorageGBPriceCents == nil || body.AudioTranscriptionPriceCents == nil {
		writeJSONError(ctx, iris.StatusBadRequest, "informe o preço de todos os adicionais")
		return
	}
	prices, err := repo.UpdateAddonPrices(repo.AddonPrices{
		ExtraConnectionPriceCents: *body.ExtraConnectionPriceCents, ExtraAgentPriceCents: *body.ExtraAgentPriceCents,
		ExtraStorageGBPriceCents: *body.ExtraStorageGBPriceCents, AudioTranscriptionPriceCents: *body.AudioTranscriptionPriceCents,
	})
	if errors.Is(err, repo.ErrInvalidAddonPrice) {
		writeJSONError(ctx, iris.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível salvar os preços dos adicionais")
		return
	}
	ctx.JSON(iris.Map{"data": prices})
}
