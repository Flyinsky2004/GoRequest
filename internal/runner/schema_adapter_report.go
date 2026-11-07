package runner

import "gorequest/internal/core"

// redactReport closes the A01/A02 report boundary after all workers finish.
// Step-level masking alone cannot protect dataset values and saved metadata,
// nor earlier results from a sensitive value discovered by a later step.
// Derived hashes, numeric facts, timestamps and verdicts remain unchanged.
func (r *Runner) redactReport(result *RunResult) {
	if result == nil {
		return
	}
	r.registerBoundSecrets()
	redact := r.red.RedactString
	failure := func(f *Failure) {
		if f != nil {
			f.Message = redact(f.Message)
		}
	}
	assertions := func(items []AssertionResult) {
		for i := range items {
			a := &items[i]
			a.Target = redact(a.Target)
			a.Message = redact(a.Message)
			a.Expected = redact(a.Expected)
			a.Actual = redact(a.Actual)
		}
	}
	result.ProjectID = redact(result.ProjectID)
	result.ProjectName = redact(result.ProjectName)
	result.SuiteID = redact(result.SuiteID)
	result.SuiteName = redact(result.SuiteName)
	result.Environment = redact(result.Environment)
	failure(result.Failure)
	if result.Dataset != nil {
		result.Dataset.File = redact(result.Dataset.File)
		for i := range result.Dataset.Fields {
			result.Dataset.Fields[i] = redact(result.Dataset.Fields[i])
		}
	}
	for i := range result.Rows {
		row := &result.Rows[i]
		row.Reason = redact(row.Reason)
		data := make(map[string]string, len(row.Data))
		for key, value := range row.Data {
			if sensitiveJSONKeyPattern.MatchString(key) {
				value = "***"
			}
			data[redact(key)] = redact(value)
		}
		row.Data = data
		for j := range row.Steps {
			step := &row.Steps[j]
			step.ID = redact(step.ID)
			step.Name = redact(step.Name)
			step.Request = redact(step.Request)
			step.Reason = redact(step.Reason)
			failure(step.Failure)
			assertions(step.Assertions)
			for k := range step.Attempts {
				attempt := &step.Attempts[k]
				attempt.FinalURL = redact(attempt.FinalURL)
				attempt.Proto = redact(attempt.Proto)
				attempt.ContentType = redact(attempt.ContentType)
				failure(attempt.Failure)
				assertions(attempt.Assertions)
				for h := range attempt.Headers {
					attempt.Headers[h].Name = redact(attempt.Headers[h].Name)
					attempt.Headers[h].Value = redact(attempt.Headers[h].Value)
				}
				for e := range attempt.Extracted {
					attempt.Extracted[e] = redact(attempt.Extracted[e])
				}
			}
			for k := range step.Extractions {
				e := &step.Extractions[k]
				e.Name = redact(e.Name)
				e.Target = redact(e.Target)
				e.Value = redact(e.Value)
				e.Message = redact(e.Message)
			}
		}
	}
}

// MapSecrets is an existing concrete provider, not a SecretNames implementer.
// Handle both forms without changing Core's credential interfaces.
func (r *Runner) registerBoundSecrets() {
	var names []string
	switch provider := r.opts.Secrets.(type) {
	case secretEnumerator:
		names = provider.SecretNames()
	case core.MapSecrets:
		for name := range provider {
			names = append(names, name)
		}
	}
	for _, name := range names {
		if value, found := r.opts.Secrets.Get(name); found && value != "" && !containsString(r.knownSecrets, value) {
			r.knownSecrets = append(r.knownSecrets, value)
			r.red.RegisterSecret(value)
		}
	}
}
