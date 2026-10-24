# Fine-tuning command removal

The CLI no longer exposes fine-tuning commands, including their colon aliases.
This removes jobs, events, checkpoints, checkpoint permissions, and alpha graders.
Existing scripts must call the Platform API directly or use an OpenAI SDK.

For example, the Python SDK reads the API key from `OPENAI_API_KEY`:

```python
from openai import OpenAI

page = OpenAI().fine_tuning.jobs.list(limit=20)
for job in page.data:
    print(job.id, job.status)
```

This CLI change does not change API access or model availability.
The [official fine-tuning notice](https://developers.openai.com/api/docs/deprecations#update-to-openais-self-serve-fine-tuning)
defines eligibility restrictions and retirement dates.

Files, models, inference, usage records, and webhook event data remain available through their existing commands.
