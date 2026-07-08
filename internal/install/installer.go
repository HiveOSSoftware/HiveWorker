package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"hivepanel-worker/internal/comb"
	"hivepanel-worker/internal/files"
)

type Logger func(line string)

type Context struct {
	InstanceDir string
	Variables   map[string]string
	Saved       map[string]any
	Log         Logger
}

func Run(instanceDir string, variables map[string]string, steps []comb.InstallStep, log Logger) error {
	ctx := &Context{
		InstanceDir: instanceDir,
		Variables:   variables,
		Saved:       map[string]any{},
		Log:         log,
	}

	for _, step := range steps {
		log("Running install step: " + step.Type)

		var result any
		var err error

		switch step.Type {
		case "mkdir":
			result, err = stepMkdir(ctx, step)

		case "write_file":
			result, err = stepWriteFile(ctx, step)

		case "download":
			result, err = stepDownload(ctx, step)

		case "run":
			result, err = stepRun(ctx, step)

		case "http":
			result, err = stepHTTP(ctx, step)

		default:
			err = errors.New("unknown install step type: " + step.Type)
		}

		if err != nil {
			return err
		}

		if step.Save != "" {
			ctx.Saved[step.Save] = result
		}
	}

	return nil
}

func stepMkdir(ctx *Context, step comb.InstallStep) (any, error) {
	target := render(ctx, getString(step.With, "target"))

	if target == "" {
		return nil, errors.New("mkdir target is required")
	}

	ctx.Log("Creating folder: " + target)

	path, err := files.SafePath(ctx.InstanceDir, target)
	if err != nil {
		return nil, err
	}

	return map[string]any{"target": target}, os.MkdirAll(path, 0755)
}

func stepWriteFile(ctx *Context, step comb.InstallStep) (any, error) {
	target := render(ctx, getString(step.With, "target"))
	content := render(ctx, getString(step.With, "content"))

	if target == "" {
		return nil, errors.New("write_file target is required")
	}

	ctx.Log("Writing file: " + target)

	err := files.Write(ctx.InstanceDir, target, content)

	return map[string]any{
		"target": target,
		"bytes":  len(content),
	}, err
}

func stepDownload(ctx *Context, step comb.InstallStep) (any, error) {
	url := render(ctx, getString(step.With, "url"))
	target := render(ctx, getString(step.With, "target"))

	if url == "" {
		return nil, errors.New("download url is required")
	}

	if target == "" {
		return nil, errors.New("download target is required")
	}

	ctx.Log("Downloading: " + url)

	response, err := httpGet(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("download failed with status: " + response.Status)
	}

	targetPath, err := files.SafePath(ctx.InstanceDir, target)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return nil, err
	}

	out, err := os.Create(targetPath)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	size, err := io.Copy(out, response.Body)
	if err != nil {
		return nil, err
	}

	ctx.Log("Downloaded to: " + target)

	return map[string]any{
		"url":    url,
		"target": target,
		"bytes":  size,
	}, nil
}

func stepRun(ctx *Context, step comb.InstallStep) (any, error) {
	command := render(ctx, getString(step.With, "command"))

	if command == "" {
		return nil, errors.New("run command is required")
	}

	ctx.Log("Running command: " + command)

	cmd := shellCommand(command)
	cmd.Dir = ctx.InstanceDir

	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		ctx.Log(string(output))
	}

	return map[string]any{
		"command": command,
		"output":  string(output),
	}, err
}

func stepHTTP(ctx *Context, step comb.InstallStep) (any, error) {
	url := render(ctx, getString(step.With, "url"))

	if url == "" {
		return nil, errors.New("http url is required")
	}

	ctx.Log("HTTP GET: " + url)

	response, err := httpGet(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("http request failed with status: " + response.Status)
	}

	var data any
	if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
		return nil, err
	}

	return data, nil
}

func getString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}

	value, exists := values[key]
	if !exists || value == nil {
		return ""
	}

	return fmt.Sprint(value)
}

func render(ctx *Context, input string) string {
	output := input

	for key, value := range ctx.Variables {
		output = strings.ReplaceAll(output, "{{"+key+"}}", value)
	}

	for key, value := range ctx.Saved {
		flattened := flatten(key, value)

		for flatKey, flatValue := range flattened {
			output = strings.ReplaceAll(output, "{{"+flatKey+"}}", flatValue)
		}
	}

	return output
}

func flatten(prefix string, value any) map[string]string {
	result := map[string]string{}

	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			for nestedKey, nestedValue := range flatten(prefix+"."+key, nested) {
				result[nestedKey] = nestedValue
			}
		}

	case []any:
		for index, nested := range typed {
			for nestedKey, nestedValue := range flatten(fmt.Sprintf("%s.%d", prefix, index), nested) {
				result[nestedKey] = nestedValue
			}
		}

	default:
		result[prefix] = fmt.Sprint(typed)
	}

	return result
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("powershell", "-Command", command)
	}

	return exec.Command("bash", "-lc", command)
}

func httpGet(url string) (*http.Response, error) {
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	request.Header.Set("User-Agent", "HivePanel-Worker/0.1 (contact: support@hivepanel.local)")

	return http.DefaultClient.Do(request)
}
