package handler

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// ExifTool is a wrapper around running exiftool binary and it's streams
type ExifTool struct {
	cmd *exec.Cmd
	// We need cancel function to be able to terminate exiftool process early if we
	// face XML decoding error
	cancel func()
	stdout io.ReadCloser
}

// Listx returns an `exiftool -listx` command ready to get started
func Listx(ctx context.Context) *ExifTool {
	ctx, cancel := context.WithCancel(ctx)
	return &ExifTool{
		cmd:    exec.CommandContext(ctx, "exiftool", "-listx"),
		cancel: cancel,
	}

}

// Close closes the exiftool command
func (et *ExifTool) Close() {
	if et.stdout != nil {
		et.stdout.Close()
	}
}

// Start starts the exiftool and leaves it running in the background
func (et *ExifTool) Start() error {
	var err error
	et.stdout, err = et.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("et.cmd.StdoutPipe: %w", err)
	}

	err = et.cmd.Start()
	if err != nil {
		return fmt.Errorf("et.cmd.Start: %w", err)
	}

	return nil
}

// StreamTags streams tags from a running exiftool to a given writer.
// exiftool must be started
func (et *ExifTool) StreamTags(w io.Writer) (err error) {
	decoder := xml.NewDecoder(et.stdout)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s := &streamer{
			Decoder: decoder,
			Writer:  w,
			Encoder: json.NewEncoder(w),
		}

		prologErr := s.emitProlog()
		if prologErr != nil {
			err = fmt.Errorf("s.emitProlog: %w", prologErr)
			return
		}

		// We don't need to pass context down the streaming function because if
		// the request is canceled (network connection is broken), ExifTool context
		// would be cancelled, which would terminate exiftool process and subsequent
		// streaming reads will fail, so s.stream would exit (albeit with an error)
		streamErr := s.stream()
		if errors.Is(streamErr, io.EOF) {
			// That's okay, we've just reached the end of input. I'm treating EOF
			// in functions down the call chain like any other error to avoid
			// cluttering the code with io.EOF checks.

			epilogErr := s.emitEpilog()
			if epilogErr != nil {
				err = fmt.Errorf("s.emitEpilog: %w", epilogErr)
				return
			}

			return
		}
		if streamErr != nil {
			err = fmt.Errorf("stream: %w", streamErr)
			// Cancel the process context to make sure that process is killed
			// as soon as possible
			et.cancel()
			return
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		waitErr := et.cmd.Wait()
		// In case there already is an error we don't overwrite it because it's
		// either "signal: killed" because of the context cancellation above or
		// an extremely rare case of having two errors simultaneously (one in
		// streamer, another in OS/exiftool) and for simplicity we just prefer the
		// streamer one.
		//
		// In a production code, I would have multierr.Combine here for the
		// case when both errors are not nil
		if waitErr != nil && err == nil {
			err = fmt.Errorf("et.cmd.Wait: %w", waitErr)
		}
	}()

	wg.Wait()

	return
}

type streamer struct {
	Decoder *xml.Decoder
	Writer  io.Writer
	Encoder *json.Encoder
}

func (s *streamer) stream() (err error) {
	for {
		table, err := search(xmlQuery{NamedStart: "table"}, s.Decoder)
		if err != nil {
			return fmt.Errorf("search: %v", err)
		}
		tableName, hasTableName := attributeByLocalName("name", table.NamedStart)
		if !hasTableName {
			return errors.New("no table name specified by exiftool")
		}
		err = s.streamTags(tableName)
		if err != nil {
			return fmt.Errorf("s.streamTags: %w", err)
		}
	}
}

func (s *streamer) emitProlog() error {
	_, err := s.Writer.Write([]byte(`{"tags": [`))
	if err != nil {
		return fmt.Errorf("s.Writer.Write: %w", err)
	}
	return nil
}

func (s *streamer) emitEpilog() error {
	_, err := s.Writer.Write([]byte(`]}`))
	if err != nil {
		return fmt.Errorf("s.Writer.Write: %w", err)
	}
	return nil
}

func (s *streamer) streamTags(tableName string) error {
	comma := false
	for {
		res, err := search(xmlQuery{
			NamedStart: "tag",
			NamedEnd:   "table",
		}, s.Decoder)
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
		if res.NamedEnd != nil {
			// We're done reading the table, go on with the next one
			return nil
		}
		if res.NamedStart != nil {
			if comma {
				_, err := s.Writer.Write([]byte(","))
				if err != nil {
					return fmt.Errorf("s.Writer.Write: %w", err)
				}
			}
			err := s.emitTag(tableName, res.NamedStart)
			if err != nil {
				return fmt.Errorf("s.emitTag: %w", err)
			}
		}
		comma = true
	}
}

func (s *streamer) emitTag(tableName string, se *xml.StartElement) error {
	type xmlDesc struct {
		Lang  string `xml:"lang,attr"`
		Value string `xml:",chardata"`
	}
	var tag struct {
		Name            string            `xml:"name,attr"json:"-"`
		Writable        bool              `xml:"writable,attr"json:"writable"`
		Path            string            `json:"path"`
		Group           string            `json:"group"`
		Description     []xmlDesc         `xml:"desc"json:"-"`
		JSONDescription map[string]string `json:"description"`
		Type            string            `xml:"type,attr"json:"type"`
	}
	err := s.Decoder.DecodeElement(&tag, se)
	if err != nil {
		return fmt.Errorf("s.Decoder.DecodeElement: %w", err)
	}
	tag.Path = tableName + ":" + tag.Name
	// I wasn't sure about this, there is no explicit statement about where
	// the value for Group comes from
	tag.Group = tableName
	tag.JSONDescription = make(map[string]string)
	for _, d := range tag.Description {
		tag.JSONDescription[d.Lang] = d.Value
	}
	err = s.Encoder.Encode(tag)
	if err != nil {
		return fmt.Errorf("s.Encoder.Encode: %w", err)
	}
	return nil
}

type xmlQuery struct {
	NamedStart string
	NamedEnd   string
}

type xmlSearchResult struct {
	NamedStart *xml.StartElement
	NamedEnd   *xml.EndElement
}

// search grabs token one by one until any of the conditions declared by
// xmlQuery are satisfied
func search(q xmlQuery, decoder *xml.Decoder) (xmlSearchResult, error) {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return xmlSearchResult{}, fmt.Errorf("decoder.Token: %w", err)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			if q.NamedStart == "" {
				continue
			}
			if v.Name.Local == q.NamedStart {
				return xmlSearchResult{
					NamedStart: &v,
				}, nil
			}
		case xml.EndElement:
			if q.NamedEnd == "" {
				continue
			}
			if v.Name.Local == q.NamedStart {
				return xmlSearchResult{
					NamedEnd: &v,
				}, nil
			}
		default:
			continue
		}
	}
}

func attributeByLocalName(name string, se *xml.StartElement) (string, bool) {
	for _, attr := range se.Attr {
		if attr.Name.Local == name {
			return attr.Value, true
		}
	}

	return "", false
}
