package llm

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Files accepted by the AI agent knowledge base. Their text is extracted for
// the operator to review; the files themselves are never stored.

const (
	MaxKnowledgeFileBytes = 10 << 20
	// Office files are zip archives: this caps what one may expand to.
	maxKnowledgeArchiveBytes = 64 << 20
	maxKnowledgeSheetCells   = 200000
	knowledgeImagePrompt     = "Extraia todo o texto visível nesta imagem, preservando títulos, listas e tabelas (use | entre colunas). " +
		"Se houver informações visuais relevantes, como produtos, preços, gráficos ou diagramas, descreva-as de forma objetiva. " +
		"Retorne somente o conteúdo, sem comentários sobre o processo."
)

// KnowledgeFileKind classifies an upload by extension, confirmed by content.
// It returns pdf, text, csv, docx, odt, pptx, xlsx, ods or image, or an error
// explaining why the file cannot be used.
func KnowledgeFileKind(name string, data []byte) (string, error) {
	extension := strings.ToLower(filepath.Ext(name))
	zipped := bytes.HasPrefix(data, []byte("PK\x03\x04"))
	switch extension {
	case ".pdf":
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return "", errors.New("o arquivo enviado não é um PDF válido")
		}
		return "pdf", nil
	case ".txt", ".md", ".markdown", ".json":
		return "text", nil
	case ".csv", ".tsv":
		return "csv", nil
	case ".docx", ".odt", ".pptx", ".xlsx", ".ods":
		if !zipped {
			return "", fmt.Errorf("o arquivo %s está corrompido ou não é um documento válido", extension)
		}
		return strings.TrimPrefix(extension, "."), nil
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		if !strings.HasPrefix(http.DetectContentType(data), "image/") {
			return "", errors.New("o arquivo enviado não é uma imagem válida")
		}
		return "image", nil
	case ".doc", ".xls", ".ppt":
		return "", fmt.Errorf("arquivos %s do Office antigo não são suportados; salve como %sx ou PDF", extension, extension)
	}
	return "", errors.New("formato não suportado; envie PDF, Word (.docx), ODT, PowerPoint (.pptx), planilha (.xlsx, .ods, .csv), TXT, Markdown ou imagem")
}

// KnowledgeImageMIME returns the content type to send an image to a provider.
func KnowledgeImageMIME(data []byte) string {
	return strings.SplitN(http.DetectContentType(data), ";", 2)[0]
}

// KnowledgeImagePrompt asks a vision model for the image's useful content.
func KnowledgeImagePrompt() string { return knowledgeImagePrompt }

// ExtractKnowledgeFile returns the text of a non-image knowledge file.
func ExtractKnowledgeFile(kind string, data []byte) (string, error) {
	var text string
	var err error
	switch kind {
	case "pdf":
		return ExtractPDFData(data)
	case "text":
		text, err = decodeKnowledgeText(data)
	case "csv":
		text, err = extractCSV(data)
	case "docx", "odt", "pptx", "xlsx", "ods":
		text, err = extractOfficeDocument(kind, data)
	default:
		return "", errors.New("formato não suportado")
	}
	if err != nil {
		return "", err
	}
	return cleanKnowledgeText(text)
}

func cleanKnowledgeText(text string) (string, error) {
	text = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' || character >= 32 {
			return character
		}
		return -1
	}, strings.ReplaceAll(text, "\r\n", "\n"))
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			if blank++; blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		kept = append(kept, line)
	}
	text = strings.TrimSpace(strings.Join(kept, "\n"))
	if text == "" {
		return "", errors.New("o arquivo não possui texto extraível")
	}
	if len(text) > MaxKnowledgePDFText {
		return "", fmt.Errorf("o texto extraído excede %d caracteres", MaxKnowledgePDFText)
	}
	return text, nil
}

// decodeKnowledgeText reads UTF-8, UTF-16 (with BOM) or, failing those,
// Windows-1252, which is how Excel and Notepad often save Portuguese text.
func decodeKnowledgeText(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return decodeUTF16(data[2:], data[0] == 0xFE), nil
	}
	if utf8.Valid(data) {
		if bytes.IndexByte(data, 0) >= 0 {
			return "", errors.New("o arquivo parece ser binário, não texto")
		}
		return string(data), nil
	}
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(data)
	if err != nil || bytes.IndexByte(decoded, 0) >= 0 {
		return "", errors.New("não foi possível ler a codificação do arquivo; salve-o como UTF-8")
	}
	return string(decoded), nil
}

func decodeUTF16(data []byte, bigEndian bool) string {
	units := make([]uint16, 0, len(data)/2)
	for index := 0; index+1 < len(data); index += 2 {
		if bigEndian {
			units = append(units, uint16(data[index])<<8|uint16(data[index+1]))
		} else {
			units = append(units, uint16(data[index+1])<<8|uint16(data[index]))
		}
	}
	return string(utf16.Decode(units))
}

func extractCSV(data []byte) (string, error) {
	text, err := decodeKnowledgeText(data)
	if err != nil {
		return "", err
	}
	firstLine, _, _ := strings.Cut(text, "\n")
	delimiter := ','
	best := strings.Count(firstLine, ",")
	for _, candidate := range []rune{';', '\t'} {
		if count := strings.Count(firstLine, string(candidate)); count > best {
			delimiter, best = candidate, count
		}
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	var output strings.Builder
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errors.New("a planilha CSV está malformada")
		}
		writeKnowledgeRow(&output, record)
	}
	return output.String(), nil
}

// writeKnowledgeRow writes one table row as "a | b | c", dropping empty rows
// and trailing empty cells.
func writeKnowledgeRow(output *strings.Builder, cells []string) {
	last := len(cells) - 1
	for last >= 0 && strings.TrimSpace(cells[last]) == "" {
		last--
	}
	if last < 0 {
		return
	}
	for index, cell := range cells[:last+1] {
		if index > 0 {
			output.WriteString(" | ")
		}
		output.WriteString(strings.Join(strings.Fields(cell), " "))
	}
	output.WriteByte('\n')
}

type knowledgeArchive struct {
	files     map[string]*zip.File
	remaining int64
}

func (archive *knowledgeArchive) read(name string) ([]byte, error) {
	file := archive.files[name]
	if file == nil {
		return nil, nil
	}
	reader, err := file.Open()
	if err != nil {
		return nil, errors.New("o documento está corrompido")
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, archive.remaining+1))
	if err != nil {
		return nil, errors.New("o documento está corrompido")
	}
	if archive.remaining -= int64(len(data)); archive.remaining < 0 {
		return nil, errors.New("o documento é grande demais depois de descompactado")
	}
	return data, nil
}

func extractOfficeDocument(kind string, data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", errors.New("o documento está corrompido")
	}
	archive := &knowledgeArchive{files: map[string]*zip.File{}, remaining: maxKnowledgeArchiveBytes}
	for _, file := range reader.File {
		archive.files[file.Name] = file
	}
	switch kind {
	case "docx":
		return extractDOCX(archive)
	case "pptx":
		return extractPPTX(archive)
	case "xlsx":
		return extractXLSX(archive)
	}
	content, err := archive.read("content.xml")
	if err != nil {
		return "", err
	}
	if content == nil {
		return "", errors.New("o documento não possui conteúdo")
	}
	return extractOpenDocument(content)
}

func extractDOCX(archive *knowledgeArchive) (string, error) {
	document, err := archive.read("word/document.xml")
	if err != nil {
		return "", err
	}
	if document == nil {
		return "", errors.New("o arquivo não é um documento do Word válido")
	}
	return extractWordprocessingXML(document)
}

// extractWordprocessingXML handles WordprocessingML (w:) and DrawingML (a:)
// text: runs in <t>, paragraphs in <p>, table cells in <tc> and rows in <tr>.
func extractWordprocessingXML(data []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var output strings.Builder
	inText := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errors.New("o documento está corrompido")
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "t":
				inText = true
			case "tab":
				output.WriteByte('\t')
			case "br", "cr":
				output.WriteByte('\n')
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "t":
				inText = false
			case "p":
				output.WriteByte('\n')
			case "tc":
				output.WriteString(" | ")
			case "tr":
				output.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				output.Write(element)
			}
		}
	}
	return output.String(), nil
}

func extractPPTX(archive *knowledgeArchive) (string, error) {
	type slide struct {
		number int
		name   string
	}
	var slides []slide
	for name := range archive.files {
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			number, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "ppt/slides/slide"), ".xml"))
			if err == nil {
				slides = append(slides, slide{number, name})
			}
		}
	}
	if len(slides) == 0 {
		return "", errors.New("a apresentação não possui slides")
	}
	sort.Slice(slides, func(a, b int) bool { return slides[a].number < slides[b].number })
	var output strings.Builder
	for _, item := range slides {
		data, err := archive.read(item.name)
		if err != nil {
			return "", err
		}
		text, err := extractWordprocessingXML(data)
		if err != nil {
			return "", err
		}
		if text = strings.TrimSpace(text); text != "" {
			fmt.Fprintf(&output, "## Slide %d\n%s\n\n", item.number, text)
		}
	}
	return output.String(), nil
}

func attribute(element xml.StartElement, local string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

func extractXLSX(archive *knowledgeArchive) (string, error) {
	shared, err := xlsxSharedStrings(archive)
	if err != nil {
		return "", err
	}
	sheets, err := xlsxSheets(archive)
	if err != nil {
		return "", err
	}
	if len(sheets) == 0 {
		return "", errors.New("a planilha não possui abas")
	}
	var output strings.Builder
	cells := 0
	for _, sheet := range sheets {
		data, err := archive.read(sheet.path)
		if err != nil {
			return "", err
		}
		if data == nil {
			continue
		}
		var body strings.Builder
		if err := xlsxSheetRows(data, shared, &body, &cells); err != nil {
			return "", err
		}
		if strings.TrimSpace(body.String()) != "" {
			fmt.Fprintf(&output, "## Planilha: %s\n%s\n", sheet.name, body.String())
		}
	}
	return output.String(), nil
}

func xlsxSharedStrings(archive *knowledgeArchive) ([]string, error) {
	data, err := archive.read("xl/sharedStrings.xml")
	if err != nil || data == nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var shared []string
	var current strings.Builder
	inText, inPhonetic := false, false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return shared, nil
		}
		if err != nil {
			return nil, errors.New("a planilha está corrompida")
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "si":
				current.Reset()
			case "t":
				inText = true
			case "rPh":
				inPhonetic = true
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "si":
				shared = append(shared, current.String())
			case "t":
				inText = false
			case "rPh":
				inPhonetic = false
			}
		case xml.CharData:
			if inText && !inPhonetic {
				current.Write(element)
			}
		}
	}
}

type xlsxSheet struct{ name, path string }

func xlsxSheets(archive *knowledgeArchive) ([]xlsxSheet, error) {
	workbook, err := archive.read("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	relations, err := archive.read("xl/_rels/workbook.xml.rels")
	if err != nil {
		return nil, err
	}
	targets := map[string]string{}
	if relations != nil {
		var parsed struct {
			Relationships []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(relations, &parsed); err != nil {
			return nil, errors.New("a planilha está corrompida")
		}
		for _, relation := range parsed.Relationships {
			target := relation.Target
			if strings.HasPrefix(target, "/") {
				target = strings.TrimPrefix(target, "/")
			} else {
				target = path.Join("xl", target)
			}
			targets[relation.ID] = target
		}
	}
	var sheets []xlsxSheet
	if workbook != nil {
		decoder := xml.NewDecoder(bytes.NewReader(workbook))
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, errors.New("a planilha está corrompida")
			}
			if element, ok := token.(xml.StartElement); ok && element.Name.Local == "sheet" {
				if target := targets[attribute(element, "id")]; target != "" {
					sheets = append(sheets, xlsxSheet{name: attribute(element, "name"), path: target})
				}
			}
		}
	}
	if len(sheets) == 0 {
		for name := range archive.files {
			if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") && !strings.Contains(strings.TrimPrefix(name, "xl/worksheets/"), "/") {
				sheets = append(sheets, xlsxSheet{name: strings.TrimSuffix(path.Base(name), ".xml"), path: name})
			}
		}
		sort.Slice(sheets, func(a, b int) bool { return sheets[a].path < sheets[b].path })
	}
	return sheets, nil
}

// xlsxColumn turns a cell reference such as "AB12" into a zero-based column.
func xlsxColumn(reference string) int {
	column := 0
	for _, character := range reference {
		if character < 'A' || character > 'Z' {
			break
		}
		column = column*26 + int(character-'A'+1)
	}
	return column - 1
}

func xlsxSheetRows(data []byte, shared []string, output *strings.Builder, cells *int) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var row []string
	var value strings.Builder
	cellType, column := "", 0
	inValue := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("a planilha está corrompida")
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "row":
				row = row[:0]
			case "c":
				cellType = attribute(element, "t")
				column = len(row)
				if reference := attribute(element, "r"); reference != "" {
					if parsed := xlsxColumn(reference); parsed >= 0 && parsed < 16384 {
						column = parsed
					}
				}
				value.Reset()
			case "v", "t":
				inValue = true
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "v", "t":
				inValue = false
			case "c":
				text := value.String()
				switch cellType {
				case "s":
					if index, err := strconv.Atoi(strings.TrimSpace(text)); err == nil && index >= 0 && index < len(shared) {
						text = shared[index]
					}
				case "b":
					text = map[string]string{"0": "FALSO", "1": "VERDADEIRO"}[strings.TrimSpace(text)]
				}
				if strings.TrimSpace(text) == "" {
					continue
				}
				if *cells++; *cells > maxKnowledgeSheetCells {
					return fmt.Errorf("a planilha possui mais de %d células preenchidas", maxKnowledgeSheetCells)
				}
				for len(row) < column {
					row = append(row, "")
				}
				if column < len(row) {
					row[column] = text
				} else {
					row = append(row, text)
				}
			case "row":
				writeKnowledgeRow(output, row)
			}
		case xml.CharData:
			if inValue {
				value.Write(element)
			}
		}
	}
}

// extractOpenDocument reads the content.xml of ODT and ODS files: paragraphs
// and headings become lines, and each table (a sheet in ODS) becomes rows of
// "a | b | c" under its name.
func extractOpenDocument(data []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var output, cell strings.Builder
	var row []string
	cellRepeat, cells := 1, 0
	paragraphDepth, cellDepth := 0, 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return output.String(), nil
		}
		if err != nil {
			return "", errors.New("o documento está corrompido")
		}
		target := &output
		if cellDepth > 0 {
			target = &cell
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "table":
				if name := attribute(element, "name"); name != "" {
					fmt.Fprintf(&output, "\n## %s\n", name)
				}
			case "table-row":
				row = row[:0]
			case "table-cell", "covered-table-cell":
				cellDepth++
				cell.Reset()
				cellRepeat = 1
				if repeat, err := strconv.Atoi(attribute(element, "number-columns-repeated")); err == nil && repeat > 1 {
					cellRepeat = min(repeat, 100)
				}
			case "p", "h":
				paragraphDepth++
				if cellDepth > 0 && cell.Len() > 0 {
					cell.WriteByte(' ')
				}
			case "tab":
				target.WriteByte('\t')
			case "s":
				count := 1
				if parsed, err := strconv.Atoi(attribute(element, "c")); err == nil && parsed > 1 {
					count = min(parsed, 50)
				}
				target.WriteString(strings.Repeat(" ", count))
			case "line-break":
				if cellDepth > 0 {
					cell.WriteByte(' ')
				} else {
					output.WriteByte('\n')
				}
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "p", "h":
				paragraphDepth--
				if cellDepth == 0 {
					output.WriteByte('\n')
				}
			case "table-cell", "covered-table-cell":
				cellDepth--
				text := cell.String()
				// Empty cells often repeat to the sheet's edge (capped above);
				// trailing ones are dropped with the row.
				if strings.TrimSpace(text) != "" {
					cells += cellRepeat
				}
				if cells > maxKnowledgeSheetCells {
					return "", fmt.Errorf("a planilha possui mais de %d células preenchidas", maxKnowledgeSheetCells)
				}
				for range cellRepeat {
					row = append(row, text)
				}
			case "table-row":
				writeKnowledgeRow(&output, row)
			}
		case xml.CharData:
			if paragraphDepth > 0 {
				target.Write(element)
			}
		}
	}
}
