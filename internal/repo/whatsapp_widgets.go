package repo

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrWhatsAppWidgetConflict = errors.New("whatsapp widget revision conflict")

type WhatsAppWidget struct {
	Id           int       `json:"id"`
	CompanyId    int       `json:"companyId"`
	PublicId     uuid.UUID `json:"publicId"`
	Name         string    `json:"name"`
	PhoneNumber  string    `json:"phoneNumber"`
	CallToAction string    `json:"callToAction"`
	Color        string    `json:"color"`
	IconOnly     bool      `json:"iconOnly"`
	IsActive     bool      `json:"isActive"`
	Revision     int       `json:"revision"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type WhatsAppWidgetInput struct {
	Name         string `json:"name"`
	PhoneNumber  string `json:"phoneNumber"`
	CallToAction string `json:"callToAction"`
	Color        string `json:"color"`
	IconOnly     bool   `json:"iconOnly"`
	IsActive     bool   `json:"isActive"`
	Revision     int    `json:"revision"`
}

func ListWhatsAppWidgets(companyID int) ([]WhatsAppWidget, error) {
	rows, err := db.Query(`SELECT id, "companyId", "publicId", name, "phoneNumber", "callToAction", color, "iconOnly",
		"isActive", revision, "createdAt", "updatedAt" FROM whatsapp_widgets
		WHERE "companyId" = $1 ORDER BY "createdAt" DESC, id DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	widgets := []WhatsAppWidget{}
	for rows.Next() {
		widget, err := scanWhatsAppWidget(rows)
		if err != nil {
			return nil, err
		}
		widgets = append(widgets, widget)
	}
	return widgets, rows.Err()
}

func GetActiveWhatsAppWidget(publicID uuid.UUID) (WhatsAppWidget, error) {
	return scanWhatsAppWidget(db.QueryRow(`SELECT w.id, w."companyId", w."publicId", w.name, w."phoneNumber", w."callToAction", w.color, w."iconOnly",
		w."isActive", w.revision, w."createdAt", w."updatedAt" FROM whatsapp_widgets w
		JOIN companies c ON c.id = w."companyId"
		WHERE w."publicId" = $1 AND w."isActive" = true AND c."isActive" = true`, publicID))
}

func CreateWhatsAppWidget(companyID int, input WhatsAppWidgetInput) (WhatsAppWidget, error) {
	return scanWhatsAppWidget(db.QueryRow(`INSERT INTO whatsapp_widgets
		("companyId", "publicId", name, "phoneNumber", "callToAction", color, "iconOnly", "isActive")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, "companyId", "publicId", name, "phoneNumber", "callToAction", color, "iconOnly",
			"isActive", revision, "createdAt", "updatedAt"`, companyID, uuid.New(), input.Name,
		input.PhoneNumber, input.CallToAction, input.Color, input.IconOnly, input.IsActive))
}

func UpdateWhatsAppWidget(id, companyID int, input WhatsAppWidgetInput) (WhatsAppWidget, error) {
	widget, err := scanWhatsAppWidget(db.QueryRow(`UPDATE whatsapp_widgets SET name = $1, "phoneNumber" = $2,
		"callToAction" = $3, color = $4, "iconOnly" = $5, "isActive" = $6, revision = revision + 1, "updatedAt" = now()
		WHERE id = $7 AND "companyId" = $8 AND revision = $9
		RETURNING id, "companyId", "publicId", name, "phoneNumber", "callToAction", color, "iconOnly",
			"isActive", revision, "createdAt", "updatedAt"`, input.Name, input.PhoneNumber,
		input.CallToAction, input.Color, input.IconOnly, input.IsActive, id, companyID, input.Revision))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if existsErr := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM whatsapp_widgets WHERE id = $1 AND "companyId" = $2)`, id, companyID).Scan(&exists); existsErr != nil {
			return WhatsAppWidget{}, existsErr
		}
		if exists {
			return WhatsAppWidget{}, ErrWhatsAppWidgetConflict
		}
	}
	return widget, err
}

func DeleteWhatsAppWidget(id, companyID int) (bool, error) {
	result, err := db.Exec(`DELETE FROM whatsapp_widgets WHERE id = $1 AND "companyId" = $2`, id, companyID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func scanWhatsAppWidget(scanner rowScanner) (WhatsAppWidget, error) {
	var widget WhatsAppWidget
	err := scanner.Scan(&widget.Id, &widget.CompanyId, &widget.PublicId, &widget.Name, &widget.PhoneNumber,
		&widget.CallToAction, &widget.Color, &widget.IconOnly, &widget.IsActive, &widget.Revision, &widget.CreatedAt, &widget.UpdatedAt)
	return widget, err
}
