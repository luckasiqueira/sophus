package repo

import "testing"

func TestCheckMessageTextSupportsTopLevelText(t *testing.T) {
	var msg EventMessageEVO
	msg.Data.Message.Text = "mensagem atual"

	if got := CheckMessageText(msg); got != "mensagem atual" {
		t.Fatalf("CheckMessageText() = %q, want %q", got, "mensagem atual")
	}
}

func TestCheckMessageTextUsesVideoCaption(t *testing.T) {
	var msg EventMessageEVO
	msg.Data.Message.VID.Caption = "legenda do vídeo"

	if got := CheckMessageText(msg); got != "legenda do vídeo" {
		t.Fatalf("CheckMessageText() = %q, want %q", got, "legenda do vídeo")
	}
}
