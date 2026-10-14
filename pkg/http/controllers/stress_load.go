package controllers

import (
	"errors"
	"log"
	"strconv"

	"sophus/internal/monitor"
	"sophus/internal/repo"
	"sophus/internal/stresstest"

	"github.com/kataras/iris/v12"
)

// requireStressAdmin hides every stress endpoint unless STRESS_TEST_ENABLED
// is set, then requires an active platform administrator.
func requireStressAdmin(ctx iris.Context) (repo.PlatformAdmin, bool) {
	if !stresstest.Enabled() {
		ctx.StopWithStatus(iris.StatusNotFound)
		return repo.PlatformAdmin{}, false
	}
	return requirePlatformAdmin(ctx)
}

func GetStressJob(ctx iris.Context) {
	if _, ok := requireStressAdmin(ctx); !ok {
		return
	}
	ctx.JSON(iris.Map{"data": repo.CurrentStressJob(), "maxMessages": repo.StressMessagesMax})
}

func ListStressCompanyAgents(ctx iris.Context) {
	if _, ok := requireStressAdmin(ctx); !ok {
		return
	}
	companyID, ok := routeID(ctx)
	if !ok {
		return
	}
	agents, err := repo.ListAgentsByCompany(companyID)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os usuários da empresa")
		return
	}
	ctx.JSON(iris.Map{"data": agents})
}

func ListStressContacts(ctx iris.Context) {
	if _, ok := requireStressAdmin(ctx); !ok {
		return
	}
	companyID, ok := routeID(ctx)
	if !ok {
		return
	}
	contacts, err := repo.ListStressContacts(companyID)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar os contatos de teste")
		return
	}
	ctx.JSON(iris.Map{"data": contacts})
}

func StartStressGeneration(ctx iris.Context) {
	admin, ok := requireStressAdmin(ctx)
	if !ok {
		return
	}
	var body struct {
		CompanyID int `json:"companyId"`
		AgentID   int `json:"agentId"`
		Count     int `json:"count"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if body.CompanyID <= 0 || body.AgentID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "selecione a empresa e o usuário")
		return
	}
	if body.Count < 1 || body.Count > repo.StressMessagesMax {
		writeJSONError(ctx, iris.StatusBadRequest, "a quantidade deve estar entre 1 e "+strconv.Itoa(repo.StressMessagesMax))
		return
	}
	job, err := repo.StartStressGeneration(body.CompanyID, body.AgentID, body.Count)
	if err != nil {
		writeStressJobError(ctx, err, "não foi possível iniciar o teste de estresse")
		return
	}
	recordStressAudit(ctx, admin, body.CompanyID, "stress_test_generate", job)
	ctx.StatusCode(iris.StatusAccepted)
	ctx.JSON(iris.Map{"data": job})
}

func CancelStressJob(ctx iris.Context) {
	if _, ok := requireStressAdmin(ctx); !ok {
		return
	}
	if !repo.CancelStressJob() {
		writeJSONError(ctx, iris.StatusConflict, "não há teste de estresse em execução")
		return
	}
	ctx.JSON(iris.Map{"data": repo.CurrentStressJob()})
}

func DeleteStressContact(ctx iris.Context) {
	admin, ok := requireStressAdmin(ctx)
	if !ok {
		return
	}
	companyID, ok := routeID(ctx)
	if !ok {
		return
	}
	contactID, err := ctx.Params().GetInt("contactId")
	if err != nil || contactID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "contato inválido")
		return
	}
	job, err := repo.StartStressCleanup(companyID, contactID)
	if err != nil {
		writeStressJobError(ctx, err, "não foi possível remover o contato de teste")
		return
	}
	recordStressAudit(ctx, admin, companyID, "stress_test_cleanup", job)
	ctx.StatusCode(iris.StatusAccepted)
	ctx.JSON(iris.Map{"data": job})
}

func writeStressJobError(ctx iris.Context, err error, fallback string) {
	switch {
	case errors.Is(err, repo.ErrStressJobRunning):
		writeJSONError(ctx, iris.StatusConflict, err.Error())
	case errors.Is(err, repo.ErrStressAgentNotFound), errors.Is(err, repo.ErrStressContactNotFound):
		writeJSONError(ctx, iris.StatusNotFound, err.Error())
	default:
		log.Printf("stress test request failed: %v", err)
		writeJSONError(ctx, iris.StatusInternalServerError, fallback)
	}
}

func recordStressAudit(ctx iris.Context, admin repo.PlatformAdmin, companyID int, action string, job repo.StressJob) {
	if err := repo.RecordAuditEvent(repo.AuditEvent{CompanyID: &companyID, ActorID: &admin.Id, ActorEmail: admin.Email,
		ActorType: "platform", Action: action, TargetType: "contact", TargetID: strconv.Itoa(job.ContactID),
		TargetName: job.ContactName + " · " + strconv.Itoa(job.Total) + " mensagens", IPAddress: ctx.RemoteAddr()}); err != nil {
		log.Printf("stress test audit failed: company=%d action=%s: %v", companyID, action, err)
	}
}

func GetStressSummary(ctx iris.Context) {
	if _, ok := requireStressAdmin(ctx); !ok {
		return
	}
	companyID, err := ctx.Params().GetInt("id")
	if err != nil || companyID < 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "empresa inválida")
		return
	}
	summary, err := repo.GetStressSummary(companyID)
	if err != nil {
		log.Printf("stress summary failed: company=%d error=%v", companyID, err)
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar os dados de teste")
		return
	}
	ctx.JSON(iris.Map{"data": summary})
}

// RevertStressTest stops a simulation running in the same scope and then
// deletes everything the stress tools created there (companyId 0 = all).
func RevertStressTest(ctx iris.Context) {
	admin, ok := requireStressAdmin(ctx)
	if !ok {
		return
	}
	var body struct {
		CompanyID int `json:"companyId"`
	}
	if err := ctx.ReadJSON(&body); err != nil || body.CompanyID < 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "empresa inválida")
		return
	}
	if status := stresstest.CurrentStatus(); status != nil && status.Running && (body.CompanyID == 0 || status.Config.CompanyID == body.CompanyID) {
		stresstest.Stop("teste revertido")
	}
	job, err := repo.StartStressRevert(body.CompanyID)
	if err != nil {
		writeStressJobError(ctx, err, "não foi possível reverter o teste")
		return
	}
	var companyID *int
	if body.CompanyID > 0 {
		companyID = &body.CompanyID
	}
	if err := repo.RecordAuditEvent(repo.AuditEvent{CompanyID: companyID, ActorID: &admin.Id, ActorEmail: admin.Email,
		ActorType: "platform", Action: "stress_test_revert", TargetType: "stress_test", TargetID: strconv.Itoa(body.CompanyID),
		TargetName: job.ContactName, IPAddress: ctx.RemoteAddr()}); err != nil {
		log.Printf("stress test audit failed: company=%d action=stress_test_revert: %v", body.CompanyID, err)
	}
	ctx.StatusCode(iris.StatusAccepted)
	ctx.JSON(iris.Map{"data": job})
}

func StartStressSimulation(ctx iris.Context) {
	admin, ok := requireStressAdmin(ctx)
	if !ok {
		return
	}
	var config stresstest.Config
	if err := ctx.ReadJSON(&config); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if err := config.Validate(); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, err.Error())
		return
	}
	status, err := stresstest.Start(config)
	if errors.Is(err, stresstest.ErrSimulationRunning) {
		writeJSONError(ctx, iris.StatusConflict, err.Error())
		return
	}
	if err != nil {
		log.Printf("stress simulation start failed: company=%d error=%v", config.CompanyID, err)
		writeJSONError(ctx, iris.StatusInternalServerError, err.Error())
		return
	}
	companyID := config.CompanyID
	if err := repo.RecordAuditEvent(repo.AuditEvent{CompanyID: &companyID, ActorID: &admin.Id, ActorEmail: admin.Email,
		ActorType: "platform", Action: "stress_test_simulate", TargetType: "stress_test", TargetID: strconv.Itoa(companyID),
		TargetName: strconv.Itoa(config.Agents) + " agentes · " + strconv.Itoa(config.Instances) + " instâncias", IPAddress: ctx.RemoteAddr()}); err != nil {
		log.Printf("stress test audit failed: company=%d action=stress_test_simulate: %v", companyID, err)
	}
	ctx.StatusCode(iris.StatusAccepted)
	ctx.JSON(iris.Map{"data": status})
}

func StopStressSimulation(ctx iris.Context) {
	if _, ok := requireStressAdmin(ctx); !ok {
		return
	}
	if !stresstest.Stop("interrompida pelo administrador") {
		writeJSONError(ctx, iris.StatusConflict, "não há simulação em execução")
		return
	}
	ctx.JSON(iris.Map{"data": stresstest.CurrentStatus()})
}

// GetStressMetrics returns resource samples newer than the "since" cursor
// (Unix milliseconds) together with the simulation and data job states.
func GetStressMetrics(ctx iris.Context) {
	if _, ok := requireStressAdmin(ctx); !ok {
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{
		"host": monitor.HostInfo(), "samples": monitor.Samples(sinceParam(ctx)),
		"simulation": stresstest.CurrentStatus(), "job": repo.CurrentStressJob(),
	}})
}
