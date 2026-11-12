package flowengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sophus/internal/gcalendar"
	"sophus/internal/llm"
	"sophus/internal/media"
	"sophus/internal/pix"
	"sophus/internal/repo"
)

// More AI actions: send a configured file, schedule a message, transfer to
// the least busy agent, send a Pix charge and book on Google Calendar.

const (
	maxAIScheduleDays       = 30
	maxAICalendarDaysAhead  = 90
	defaultAICalendarLength = 30
	aiCalendarSlotsTool     = "_horarios"
	aiCalendarBookTool      = "_agendar"
)

var aiMediaKindLabels = map[string]string{"image": "imagem", "video": "vídeo", "audio": "áudio", "document": "documento"}

// aiExtraActionTypes are handled in this file.
var aiExtraActionTypes = map[string]bool{"file": true, "schedule": true, "smartTransfer": true, "pix": true, "calendar": true}

func parseClock(value string) (time.Duration, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, true
}

// calendarSettings returns the action's booking rules with defaults.
func calendarSettings(action AIAction) (duration, workStart, workEnd time.Duration, days map[time.Weekday]bool) {
	minutes := action.Duration
	if minutes == 0 {
		minutes = defaultAICalendarLength
	}
	duration = time.Duration(minutes) * time.Minute
	workStart, _ = parseClock(action.WorkStart)
	if action.WorkStart == "" {
		workStart = 9 * time.Hour
	}
	workEnd, _ = parseClock(action.WorkEnd)
	if action.WorkEnd == "" {
		workEnd = 18 * time.Hour
	}
	days = map[time.Weekday]bool{}
	for _, day := range action.WorkDays {
		days[time.Weekday(day)] = true
	}
	if len(days) == 0 {
		for day := time.Monday; day <= time.Friday; day++ {
			days[day] = true
		}
	}
	return
}

func pixChargeFor(action AIAction, amount float64) pix.Charge {
	return pix.Charge{KeyType: action.PixKeyType, Key: action.PixKey, Name: action.PixName, City: action.PixCity, Amount: amount}
}

func validateAIExtraAction(action AIAction) error {
	switch action.Type {
	case "file":
		if aiMediaKindLabels[action.MediaKind] == "" {
			return errors.New("escolha o tipo do arquivo")
		}
		path := strings.TrimSpace(action.MediaPath)
		if path == "" && strings.TrimSpace(action.MediaURL) == "" {
			return errors.New("envie um arquivo ou informe uma URL")
		}
		if path != "" && filepath.Base(path) != path {
			return errors.New("arquivo inválido")
		}
		if len(action.Caption) > 4000 {
			return errors.New("legenda excede 4000 caracteres")
		}
	case "schedule":
		if action.MaxDays < 0 || action.MaxDays > maxAIScheduleDays {
			return fmt.Errorf("prazo máximo deve estar entre 1 e %d dias", maxAIScheduleDays)
		}
	case "smartTransfer":
		if action.Strategy != "" && action.Strategy != "balanced" && action.Strategy != "random" {
			return errors.New("estratégia de distribuição inválida")
		}
		if action.DepartmentID < 0 {
			return errors.New("setor inválido")
		}
	case "pix":
		switch action.AmountMode {
		case "", "none", "ai":
		case "fixed":
			if action.Amount <= 0 {
				return errors.New("informe o valor fixo do Pix")
			}
		default:
			return errors.New("modo de valor do Pix inválido")
		}
		if action.Amount < 0 || action.Amount > pix.MaxAmount || action.MaxAmount < 0 || action.MaxAmount > pix.MaxAmount {
			return fmt.Errorf("valores do Pix devem estar entre 0 e %d", pix.MaxAmount)
		}
		if _, err := pixChargeFor(action, action.Amount).Payload(); err != nil {
			return err
		}
	case "calendar":
		if _, err := gcalendar.ParseServiceAccount(action.ServiceAccount); err != nil {
			return err
		}
		if id := strings.TrimSpace(action.CalendarID); id == "" || len(id) > 300 {
			return errors.New("informe o ID da agenda")
		}
		if action.Duration != 0 && (action.Duration < 5 || action.Duration > 480) {
			return errors.New("duração deve estar entre 5 e 480 minutos")
		}
		for _, clock := range []string{action.WorkStart, action.WorkEnd} {
			if _, ok := parseClock(clock); clock != "" && !ok {
				return errors.New("horários de atendimento devem usar o formato HH:MM")
			}
		}
		duration, start, end, _ := calendarSettings(action)
		if end-start < duration {
			return errors.New("o horário de atendimento deve comportar ao menos um agendamento")
		}
		for _, day := range action.WorkDays {
			if day < 0 || day > 6 {
				return errors.New("dias de atendimento inválidos")
			}
		}
	default:
		return errors.New("tipo de ação inválido")
	}
	return nil
}

// aiExtraTools returns the tool definitions of one action. A calendar action
// becomes two tools: list free slots and book one.
func aiExtraTools(index int, action AIAction, description string) []llm.ToolDefinition {
	name := aiToolName(index, action.Name)
	switch action.Type {
	case "file":
		label := aiMediaKindLabels[action.MediaKind]
		if action.MediaName != "" {
			label += " \"" + action.MediaName + "\""
		}
		return []llm.ToolDefinition{{Name: name, Parameters: objectSchema(map[string]interface{}{}, nil),
			Description: description + "\nEnvia ao cliente o arquivo configurado (" + label + "). Ele é enviado na hora; depois, só comente o arquivo se fizer sentido."}}
	case "schedule":
		days := action.MaxDays
		if days == 0 {
			days = maxAIScheduleDays
		}
		return []llm.ToolDefinition{{Name: name, Description: description + fmt.Sprintf("\nAgenda uma mensagem para o cliente em uma data futura (lembretes e retornos combinados), em até %d dias. Use a data e hora atuais informadas nas instruções.", days),
			Parameters: objectSchema(map[string]interface{}{
				"mensagem": stringProperty("Texto que será enviado ao cliente no horário agendado."),
				"dataHora": stringProperty("Data e hora do envio no formato AAAA-MM-DD HH:MM, no fuso horário informado nas instruções."),
			}, []string{"mensagem", "dataHora"})}}
	case "smartTransfer":
		return []llm.ToolDefinition{{Name: name, Description: description + "\nTransfere o atendimento para o atendente humano disponível com menos conversas e encerra sua participação. Depois de usar, escreva somente uma mensagem curta avisando o cliente.",
			Parameters: objectSchema(map[string]interface{}{"resumo": stringProperty("Resumo objetivo da solicitação do cliente para o atendente humano.")}, []string{"resumo"})}}
	case "pix":
		properties, required := map[string]interface{}{}, []string(nil)
		detail := "sem valor definido (o cliente digita o valor)"
		switch action.AmountMode {
		case "fixed":
			detail = "no valor de " + formatBRL(action.Amount)
		case "ai":
			detail = "no valor que você informar"
			limit := "Use somente valores combinados na conversa ou informados pela base de conhecimento."
			if action.MaxAmount > 0 {
				limit += " Máximo de " + formatBRL(action.MaxAmount) + "."
			}
			properties["valor"] = stringProperty("Valor em reais, com ponto decimal (ex.: 49.90). " + limit)
			required = []string{"valor"}
		}
		return []llm.ToolDefinition{{Name: name, Parameters: objectSchema(properties, required),
			Description: description + "\nEnvia ao cliente os dados de pagamento via Pix " + detail + ": chave, código copia e cola" + map[bool]string{true: " e QR Code", false: ""}[action.SendQRCode] + ". Depois de usar, não repita o código; diga que basta copiá-lo."}}
	case "calendar":
		base := name
		if len(base) > 64-len(aiCalendarSlotsTool) {
			base = strings.TrimRight(base[:64-len(aiCalendarSlotsTool)], "_")
		}
		duration, _, _, _ := calendarSettings(action)
		return []llm.ToolDefinition{
			{Name: base + aiCalendarSlotsTool, Description: description + "\nConsulta os horários livres da agenda em um dia. Use antes de oferecer horários ao cliente.",
				Parameters: objectSchema(map[string]interface{}{"data": stringProperty("Dia a consultar no formato AAAA-MM-DD.")}, []string{"data"})},
			{Name: base + aiCalendarBookTool, Description: description + fmt.Sprintf("\nAgenda um compromisso de %d minutos na agenda, em um horário livre que o cliente confirmou.", int(duration.Minutes())),
				Parameters: objectSchema(map[string]interface{}{
					"dataHora":    stringProperty("Início no formato AAAA-MM-DD HH:MM, no fuso horário informado nas instruções."),
					"titulo":      stringProperty("Assunto curto do compromisso (opcional)."),
					"observacoes": stringProperty("Detalhes informados pelo cliente (opcional)."),
				}, []string{"dataHora"})},
		}
	}
	return nil
}

func formatBRL(value float64) string {
	text := strconv.FormatFloat(value, 'f', 2, 64)
	whole, cents, _ := strings.Cut(text, ".")
	var grouped strings.Builder
	for index, digit := range whole {
		if index > 0 && (len(whole)-index)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteRune(digit)
	}
	return "R$ " + grouped.String() + "," + cents
}

// location is the company's timezone, used to read and write dates.
func (runner *aiToolRunner) location() *time.Location {
	if runner.timezone == nil {
		name := ""
		if settings, err := repo.GetCompanyOperationSettings(runner.connection.CompanyID); err == nil {
			name = settings.Timezone
		}
		runner.timezone = scheduleTimezone(name)
	}
	return runner.timezone
}

func parseAIDateTime(value string, location *time.Location) (time.Time, error) {
	value = strings.TrimSpace(strings.Replace(value, "T", " ", 1))
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("data e hora devem usar o formato AAAA-MM-DD HH:MM")
}

var aiWeekdays = []string{"domingo", "segunda-feira", "terça-feira", "quarta-feira", "quinta-feira", "sexta-feira", "sábado"}

// aiClockPrompt tells the model the current date, needed to schedule.
func aiClockPrompt(now time.Time) string {
	return fmt.Sprintf("DATA E HORA ATUAIS: %s, %s (fuso horário %s). Use-as para interpretar \"hoje\", \"amanhã\" e datas relativas.",
		aiWeekdays[now.Weekday()], now.Format("02/01/2006 15:04"), now.Location().String())
}

func aiActionsNeedClock(actions []AIAction) bool {
	for _, action := range actions {
		if action.Type == "schedule" || action.Type == "calendar" {
			return true
		}
	}
	return false
}

func (runner *aiToolRunner) runExtra(action AIAction, arguments map[string]interface{}) (string, error) {
	companyID := runner.connection.CompanyID
	switch action.Type {
	case "file":
		return runner.sendFile(action)
	case "schedule":
		return runner.schedule(action, arguments)
	case "smartTransfer":
		if runner.pending != nil {
			return "", errors.New("já existe uma transferência, pausa ou encerramento programado para esta resposta")
		}
		var err error
		if action.DepartmentID > 0 {
			err = repo.ValidateAIAgentTransferTarget(companyID, "department", action.DepartmentID)
		} else if available, checkErr := repo.AIAgentSmartTransferAvailable(companyID, nil); checkErr != nil {
			err = checkErr
		} else if !available {
			err = errors.New("nenhum atendente ativo disponível para receber o atendimento")
		}
		if err != nil {
			return "", err
		}
		runner.pending = &pendingAIAction{action: action, summary: argumentText(arguments, "resumo")}
		return "Transferência confirmada; será concluída após sua resposta. Escreva agora somente uma mensagem curta avisando o cliente.", nil
	case "pix":
		return runner.sendPix(action, arguments)
	case "calendar":
		if action.operation == aiCalendarBookTool {
			return runner.bookCalendar(action, arguments)
		}
		return runner.calendarSlots(action, arguments)
	}
	return "", errors.New("tipo de ação inválido")
}

func (runner *aiToolRunner) sendFile(action AIAction) (string, error) {
	source, err := flowMediaSource(map[string]interface{}{"mediaPath": action.MediaPath, "mediaUrl": action.MediaURL}, "mediaPath", "mediaUrl", runner.connection.CompanyID, nil)
	if err != nil || source == "" {
		return "", errors.New("arquivo indisponível")
	}
	caption := ReplaceVariables(action.Caption, runner.actionContext(nil))
	switch action.MediaKind {
	case "audio":
		err = runner.messenger.SendAudio(runner.conversation, source)
	case "document":
		err = runner.messenger.SendDocument(runner.conversation, source, caption, action.MediaName)
	default:
		err = runner.messenger.SendMedia(runner.conversation, action.MediaKind, source, caption)
	}
	if err != nil {
		return "", err
	}
	return "Arquivo enviado ao cliente.", nil
}

func (runner *aiToolRunner) schedule(action AIAction, arguments map[string]interface{}) (string, error) {
	message := argumentText(arguments, "mensagem")
	if message == "" {
		return "", errors.New("informe a mensagem a agendar")
	}
	location := runner.location()
	dueAt, err := parseAIDateTime(argumentText(arguments, "dataHora"), location)
	if err != nil {
		return "", err
	}
	days := action.MaxDays
	if days == 0 {
		days = maxAIScheduleDays
	}
	now := time.Now().In(location)
	switch {
	case !dueAt.After(now.Add(time.Minute)):
		return "", errors.New("a data e hora precisam estar no futuro")
	case dueAt.After(now.AddDate(0, 0, days)):
		return "", fmt.Errorf("só é possível agendar até %d dias à frente", days)
	}
	if err := repo.ScheduleAIAgentMessage(runner.session.Id, runner.conversation.Id, runner.connection.CompanyID, action.ID, message, dueAt); err != nil {
		return "", err
	}
	return fmt.Sprintf("Mensagem agendada para %s, %s.", aiWeekdays[dueAt.Weekday()], dueAt.Format("02/01/2006 às 15:04")), nil
}

func (runner *aiToolRunner) sendPix(action AIAction, arguments map[string]interface{}) (string, error) {
	amount := 0.0
	switch action.AmountMode {
	case "fixed":
		amount = action.Amount
	case "ai":
		raw := strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(argumentText(arguments, "valor")), "R$")), " ", "")
		if strings.Contains(raw, ",") {
			raw = strings.ReplaceAll(strings.ReplaceAll(raw, ".", ""), ",", ".")
		}
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 {
			return "", errors.New("informe um valor válido em reais, por exemplo 49.90")
		}
		if action.MaxAmount > 0 && parsed > action.MaxAmount {
			return "", fmt.Errorf("o valor passa do máximo permitido de %s", formatBRL(action.MaxAmount))
		}
		amount = parsed
	}
	charge := pixChargeFor(action, amount)
	code, err := charge.Payload()
	if err != nil {
		return "", err
	}
	key, _ := pix.NormalizeKey(action.PixKeyType, action.PixKey)
	info := "*Pagamento via Pix*\n" + pix.KeyLabel(action.PixKeyType) + ": " + key + "\nRecebedor: " + strings.TrimSpace(action.PixName)
	if amount > 0 {
		info += "\nValor: " + formatBRL(amount)
	}
	info += "\n\nCopie o código da próxima mensagem e cole na opção *Pix copia e cola* do app do seu banco."
	if message := strings.TrimSpace(action.Message); message != "" {
		info = ReplaceVariables(message, runner.actionContext(nil)) + "\n\n" + info
	}
	if err := runner.messenger.SendText(runner.conversation, info); err != nil {
		return "", err
	}
	if action.SendQRCode {
		if err := runner.sendPixQRCode(code); err != nil {
			log.Printf("ai agent pix qr code not sent: conversation=%d error=%v", runner.conversation.Id, err)
		}
	}
	if err := runner.messenger.SendText(runner.conversation, code); err != nil {
		return "", err
	}
	result := "Dados do Pix enviados ao cliente"
	if amount > 0 {
		result += " (" + formatBRL(amount) + ")"
	}
	return result + ". Não repita o código; diga que basta copiá-lo no app do banco.", nil
}

func (runner *aiToolRunner) sendPixQRCode(code string) error {
	image, err := pix.QRCode(code)
	if err != nil {
		return err
	}
	stored, err := media.Save(runner.connection.CompanyID, "image", "pix-qrcode.png", "image/png", bytes.NewReader(image))
	if err != nil {
		return err
	}
	source, err := media.SignedURL(runner.connection.CompanyID, stored.Path)
	if err != nil {
		return err
	}
	return runner.messenger.SendMedia(runner.conversation, "image", source, "QR Code Pix")
}

func (runner *aiToolRunner) calendarAccount(action AIAction) (*gcalendar.ServiceAccount, error) {
	account, err := gcalendar.ParseServiceAccount(action.ServiceAccount)
	if err != nil {
		return nil, errors.New("a conta de serviço do Google configurada na ação é inválida")
	}
	return account, nil
}

func (runner *aiToolRunner) calendarSlots(action AIAction, arguments map[string]interface{}) (string, error) {
	location := runner.location()
	day, err := time.ParseInLocation("2006-01-02", argumentText(arguments, "data"), location)
	if err != nil {
		return "", errors.New("informe o dia no formato AAAA-MM-DD")
	}
	now := time.Now().In(location)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	if day.Before(today) || day.After(today.AddDate(0, 0, maxAICalendarDaysAhead)) {
		return "", fmt.Errorf("consulte um dia entre hoje e %d dias à frente", maxAICalendarDaysAhead)
	}
	duration, workStart, workEnd, days := calendarSettings(action)
	label := aiWeekdays[day.Weekday()] + ", " + day.Format("02/01/2006")
	if !days[day.Weekday()] {
		return "Não há atendimento em " + label + ". Ofereça outro dia.", nil
	}
	account, err := runner.calendarAccount(action)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	busy, err := account.Busy(ctx, action.CalendarID, day.Add(workStart), day.Add(workEnd))
	if err != nil {
		return "", err
	}
	slots := gcalendar.FreeSlots(day, workStart, workEnd, duration, busy, now)
	if len(slots) == 0 {
		return "Não há horários livres em " + label + ". Ofereça outro dia.", nil
	}
	times := make([]string, 0, len(slots))
	for _, slot := range slots {
		times = append(times, slot.In(location).Format("15:04"))
	}
	return fmt.Sprintf("Horários livres em %s (compromissos de %d minutos): %s.", label, int(duration.Minutes()), strings.Join(times, ", ")), nil
}

func (runner *aiToolRunner) bookCalendar(action AIAction, arguments map[string]interface{}) (string, error) {
	location := runner.location()
	start, err := parseAIDateTime(argumentText(arguments, "dataHora"), location)
	if err != nil {
		return "", err
	}
	duration, workStart, workEnd, days := calendarSettings(action)
	now := time.Now().In(location)
	midnight := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, location)
	switch {
	case !start.After(now):
		return "", errors.New("o horário precisa estar no futuro")
	case start.After(now.AddDate(0, 0, maxAICalendarDaysAhead)):
		return "", fmt.Errorf("só é possível agendar até %d dias à frente", maxAICalendarDaysAhead)
	case !days[start.Weekday()] || start.Before(midnight.Add(workStart)) || start.Add(duration).After(midnight.Add(workEnd)):
		return "", errors.New("o horário está fora do atendimento; consulte os horários livres")
	}
	account, err := runner.calendarAccount(action)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	end := start.Add(duration)
	busy, err := account.Busy(ctx, action.CalendarID, start, end)
	if err != nil {
		return "", err
	}
	for _, period := range busy {
		if start.Before(period.End) && end.After(period.Start) {
			return "", errors.New("o horário acabou de ser ocupado; consulte os horários livres de novo")
		}
	}
	contact := runner.conversation.Contact
	title := strings.TrimSpace(argumentText(arguments, "titulo"))
	if title == "" {
		title = strings.TrimSpace(action.Name)
	}
	if name := strings.TrimSpace(contact.Name); name != "" {
		title += " - " + name
	}
	description := "Agendado pelo agente de IA no atendimento via WhatsApp.\nCliente: " + contact.Name + "\nTelefone: " + contact.Number
	if notes := argumentText(arguments, "observacoes"); notes != "" {
		description += "\nObservações: " + notes
	}
	if _, err := account.CreateEvent(ctx, action.CalendarID, gcalendar.Event{
		Summary: truncateUTF8(title, 200), Description: truncateUTF8(description, 2000), Start: start, End: end, TimeZone: location.String(),
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Compromisso agendado para %s, %s, com %d minutos.", aiWeekdays[start.Weekday()], start.Format("02/01/2006 às 15:04"), int(duration.Minutes())), nil
}
