// Copyright 2023 Upbound Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package internal

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/crossplane/crossplane-runtime/pkg/errors"

	"github.com/upbound/uptest/internal/config"
	"github.com/upbound/uptest/internal/templates"
)

func newTester(ms []config.Manifest, opts *config.AutomatedTest) *tester {
	return &tester{
		options:   opts,
		manifests: ms,
	}
}

type tester struct {
	options   *config.AutomatedTest
	manifests []config.Manifest
}

func (t *tester) executeTests() error {
	if err := t.writeKuttlFiles(); err != nil {
		return errors.Wrap(err, "cannot write kuttl test files")
	}
	fmt.Println("Running kuttl tests at " + t.options.Directory)
	cmd := exec.Command("bash", "-c", fmt.Sprintf(`"${KUTTL}" test --start-kind=false --skip-cluster-delete %s --timeout %d 2>&1`, t.options.Directory, t.options.DefaultTimeout)) // #nosec G204
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "cannot start kuttl")
	}
	sc := bufio.NewScanner(stdout)
	sc.Split(bufio.ScanLines)
	for sc.Scan() {
		fmt.Println(sc.Text())
	}
	return errors.Wrap(cmd.Wait(), "kuttl failed")
}

func (t *tester) prepareConfig() (*config.TestCase, []config.Resource, error) { //nolint:gocyclo // TODO: can we break this?
	tc := &config.TestCase{
		Timeout:            t.options.DefaultTimeout,
		SetupScriptPath:    t.options.SetupScriptPath,
		TeardownScriptPath: t.options.TeardownScriptPath,
	}
	examples := make([]config.Resource, 0, len(t.manifests))

	for _, m := range t.manifests {
		obj := m.Object
		kg := strings.ToLower(obj.GroupVersionKind().Kind + "." + obj.GroupVersionKind().Group)

		example := config.Resource{
			Name:       obj.GetName(),
			Namespace:  obj.GetNamespace(),
			KindGroup:  kg,
			YAML:       m.YAML,
			Timeout:    t.options.DefaultTimeout,
			Conditions: t.options.DefaultConditions,

			// As default, used the tags field
			UpdateField:       "tags",
			UpdateValue:       `{"uptest": "update"}`,
			UpdateAssertField: "tags.uptest",
			UpdateAssertValue: "update",
		}

		var err error
		annotations := obj.GetAnnotations()
		if v, ok := annotations[config.AnnotationKeyTimeout]; ok {
			example.Timeout, err = strconv.Atoi(v)
			if err != nil {
				return nil, nil, errors.Wrap(err, "timeout value is not valid")
			}
			if example.Timeout > tc.Timeout {
				tc.Timeout = example.Timeout
			}
		}

		if v, ok := annotations[config.AnnotationKeyConditions]; ok {
			example.Conditions = strings.Split(v, ",")
		}

		if v, ok := annotations[config.AnnotationKeyPreAssertHook]; ok {
			example.PreAssertScriptPath, err = filepath.Abs(filepath.Join(filepath.Dir(m.FilePath), filepath.Clean(v)))
			if err != nil {
				return nil, nil, errors.Wrap(err, "cannot find absolute path for pre assert hook")
			}
		}

		if v, ok := annotations[config.AnnotationKeyPostAssertHook]; ok {
			example.PostAssertScriptPath, err = filepath.Abs(filepath.Join(filepath.Dir(m.FilePath), filepath.Clean(v)))
			if err != nil {
				return nil, nil, errors.Wrap(err, "cannot find absolute path for post assert hook")
			}
		}

		if v, ok := annotations[config.AnnotationKeyPreDeleteHook]; ok {
			example.PreDeleteScriptPath, err = filepath.Abs(filepath.Join(filepath.Dir(m.FilePath), filepath.Clean(v)))
			if err != nil {
				return nil, nil, errors.Wrap(err, "cannot find absolute path for pre delete hook")
			}
		}

		if v, ok := annotations[config.AnnotationKeyPostDeleteHook]; ok {
			example.PostDeleteScriptPath, err = filepath.Abs(filepath.Join(filepath.Dir(m.FilePath), filepath.Clean(v)))
			if err != nil {
				return nil, nil, errors.Wrap(err, "cannot find absolute path for post delete hook")
			}
		}

		if v, ok := annotations[config.AnnotationKeyUpdateField]; ok {
			example.UpdateField = v
		}

		if v, ok := annotations[config.AnnotationKeyUpdateValue]; ok {
			example.UpdateValue = v
		}

		if v, ok := annotations[config.AnnotationKeyUpdateAssertField]; ok {
			example.UpdateAssertField = v
		}

		if v, ok := annotations[config.AnnotationKeyUpdateAssertValue]; ok {
			example.UpdateAssertValue = v
		}

		examples = append(examples, example)
	}

	return tc, examples, nil
}

func (t *tester) writeKuttlFiles() error {
	tc, examples, err := t.prepareConfig()
	if err != nil {
		return errors.Wrap(err, "cannot build examples config")
	}

	files, err := templates.Render(tc, examples)
	if err != nil {
		return errors.Wrap(err, "cannot render kuttl templates")
	}

	for k, v := range files {
		if err := os.WriteFile(filepath.Join(filepath.Join(t.options.Directory, caseDirectory), k), []byte(v), fs.ModePerm); err != nil {
			return errors.Wrapf(err, "cannot write file %q", k)
		}
	}

	return nil
}
