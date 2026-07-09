package instances

import (
	"encoding/json"
	"sophus/internal/repo"
	"sophus/pkg/http/requests"
	"sophus/utils/env"

	"github.com/google/uuid"
)

type InstanceEVO struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Connected     bool      `json:"connected"`
	WebhookURL    string    `json:"webhookURL"`
	InstanceID    uuid.UUID `json:"instanceId"`
	ConnectionKey uuid.UUID `json:"connectionKey"` // real evogo api key
	APIToken      string    `json:"apiToken"`      // used by customer to api calls
}

type InstanceEvoResponse struct {
	Data struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	} `json:"data"`
	Message string `json:"message"`
}

func (i InstanceEVO) Create() error {
	r := requests.Request{
		URL: repo.ApiBaseURL + "/instance/create",
		Payload: map[string]any{
			"name":  i.Name,
			"token": i.Token,
		},
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       env.Backend["WPP_API_GLOBAL_TOKEN"],
		},
		Method: "POST",
	}
	err := r.Do()
	if err != nil {
		return err
	}
	var response InstanceEvoResponse
	err = json.Unmarshal(r.Response.Body, &response)
	i.ConnectionKey = uuid.MustParse(response.Data.Token)

	return nil
}

//stmt, err := repo.DB.Prepare("RETURNING id;")
//if err != nil {
//	return err
//}
//defer stmt.Close()
//err = stmt.QueryRow()
