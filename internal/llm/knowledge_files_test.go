package llm

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func zipFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func extractKnowledge(t *testing.T, name string, data []byte) string {
	t.Helper()
	kind, err := KnowledgeFileKind(name, data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text, err := ExtractKnowledgeFile(kind, data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return text
}

func expectLines(t *testing.T, text string, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(text, line) {
			t.Fatalf("missing %q in:\n%s", line, text)
		}
	}
}

func TestExtractKnowledgeDOCX(t *testing.T) {
	document := `<w:document xmlns:w="w"><w:body>
		<w:p><w:r><w:t>Política de </w:t></w:r><w:r><w:t>trocas</w:t></w:r></w:p>
		<w:p><w:r><w:t>Prazo:</w:t><w:tab/><w:t>30 dias</w:t></w:r></w:p>
		<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Plano</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Preço</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
	</w:body></w:document>`
	text := extractKnowledge(t, "manual.docx", zipFiles(t, map[string]string{"word/document.xml": document}))
	expectLines(t, text, "Política de trocas", "Prazo:\t30 dias", "Plano", "Preço")
}

func TestExtractKnowledgeXLSX(t *testing.T) {
	files := map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="r"><sheets><sheet name="Preços" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst><si><t>Produto</t></si><si><t>Valor</t></si><si><r><t>Plano </t></r><r><t>Básico</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>
			<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
			<row r="2"><c r="A2" t="s"><v>2</v></c><c r="C2"><v>49.9</v></c></row>
			<row r="3"><c r="A3" t="inlineStr"><is><t>Ativo</t></is></c><c r="B3" t="b"><v>1</v></c></row>
		</sheetData></worksheet>`,
	}
	text := extractKnowledge(t, "tabela.xlsx", zipFiles(t, files))
	expectLines(t, text, "## Planilha: Preços", "Produto | Valor", "Plano Básico |  | 49.9", "Ativo | VERDADEIRO")
}

func TestExtractKnowledgeOpenDocument(t *testing.T) {
	odt := `<office:document-content xmlns:office="o" xmlns:text="t"><office:body><office:text>
		<text:h>Horários</text:h><text:p>Segunda<text:s text:c="2"/>a sexta<text:line-break/>9h às 18h</text:p>
	</office:text></office:body></office:document-content>`
	expectLines(t, extractKnowledge(t, "horarios.odt", zipFiles(t, map[string]string{"content.xml": odt})),
		"Horários", "Segunda  a sexta", "9h às 18h")

	ods := `<office:document-content xmlns:office="o" xmlns:table="tb" xmlns:text="t"><office:body><office:spreadsheet>
		<table:table table:name="Estoque">
			<table:table-row><table:table-cell><text:p>Item</text:p></table:table-cell><table:table-cell table:number-columns-repeated="2"/><table:table-cell><text:p>Qtd</text:p></table:table-cell><table:table-cell table:number-columns-repeated="16380"/></table:table-row>
			<table:table-row table:number-rows-repeated="1048570"><table:table-cell table:number-columns-repeated="16384"/></table:table-row>
		</table:table>
	</office:spreadsheet></office:body></office:document-content>`
	text := extractKnowledge(t, "estoque.ods", zipFiles(t, map[string]string{"content.xml": ods}))
	expectLines(t, text, "## Estoque", "Item |  |  | Qtd")
}

func TestExtractKnowledgePPTX(t *testing.T) {
	files := map[string]string{
		"ppt/slides/slide2.xml":  `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>Segundo slide</a:t></a:r></a:p></p:sld>`,
		"ppt/slides/slide10.xml": `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>Décimo slide</a:t></a:r></a:p></p:sld>`,
		"ppt/slides/slide1.xml":  `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>Primeiro slide</a:t></a:r></a:p></p:sld>`,
	}
	text := extractKnowledge(t, "apresentacao.pptx", zipFiles(t, files))
	if strings.Index(text, "Primeiro") > strings.Index(text, "Segundo") || strings.Index(text, "Segundo") > strings.Index(text, "Décimo") {
		t.Fatalf("slides out of order:\n%s", text)
	}
	expectLines(t, text, "## Slide 1", "## Slide 10")
}

func TestExtractKnowledgeTextAndCSV(t *testing.T) {
	expectLines(t, extractKnowledge(t, "faq.md", []byte("\xEF\xBB\xBF# Dúvidas\r\n\r\n\r\n\r\nResposta")), "# Dúvidas\n\nResposta")
	// Windows-1252, as Notepad or Excel may save it.
	expectLines(t, extractKnowledge(t, "notas.txt", []byte("Promo\xe7\xe3o v\xe1lida")), "Promoção válida")
	expectLines(t, extractKnowledge(t, "lista.csv", []byte("nome;preço\n\"Plano; anual\";100\n;\n")), "nome | preço", "Plano; anual | 100")
	expectLines(t, extractKnowledge(t, "utf16.txt", []byte{0xFF, 0xFE, 'O', 0, 'l', 0, 0xE1, 0}), "Olá")
}

func TestKnowledgeFileKindRejectsUnsupportedFiles(t *testing.T) {
	cases := map[string][]byte{
		"antigo.doc":   []byte("\xD0\xCF\x11\xE0"),
		"falso.docx":   []byte("not a zip"),
		"falso.pdf":    []byte("not a pdf"),
		"programa.exe": []byte("MZ"),
		"foto.png":     []byte("not an image"),
	}
	for name, data := range cases {
		if _, err := KnowledgeFileKind(name, data); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	if _, err := ExtractKnowledgeFile("text", []byte("a\x00b")); err == nil {
		t.Fatal("binary content must be rejected")
	}
	png := []byte("\x89PNG\r\n\x1a\n0000")
	if kind, err := KnowledgeFileKind("foto.PNG", png); err != nil || kind != "image" || KnowledgeImageMIME(png) != "image/png" {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
}

func TestExtractKnowledgeRejectsArchiveBombs(t *testing.T) {
	huge := `<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>` + strings.Repeat("a", maxKnowledgeArchiveBytes) + `</w:t></w:r></w:p></w:body></w:document>`
	if _, err := ExtractKnowledgeFile("docx", zipFiles(t, map[string]string{"word/document.xml": huge})); err == nil {
		t.Fatal("a document that expands beyond the limit must be rejected")
	}
}
