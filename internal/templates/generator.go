package templates

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"text/template"
)

type IcesConfig struct {
	StreamID        int
	StreamName      string
	IcecastHost     string
	IcecastPort     int
	IcecastPassword string
	BaseDirectory   string
	Mountpoint      string
	Genre           string
	Description     string
	URL             string
	Bitrate         int
}

func GenerateIcesConfig(config *IcesConfig, templatePath, outputPath string) error {
	templateContent, err := ioutil.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("ices").Parse(string(templateContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, config); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}
