# Fine-tuning command removal

The CLI no longer exposes fine-tuning commands, including their colon aliases.
This removes jobs, events, checkpoints, checkpoint permissions, alpha graders, and `models delete`.
Existing scripts must call the Platform API directly or use an OpenAI SDK.

File uploads and file filters reject `fine-tune` and `fine-tune-results` purposes.
The shared `vision` purpose remains available for image inputs.
Webhook creation, subscription updates, and tests reject `fine_tuning.*` events.
Guided choices, help, completions, and command suggestions omit these features.
The `store` option remains available for stored Chat Completions and evals.

This CLI change does not change API access or model availability.
The [official fine-tuning notice](https://developers.openai.com/api/docs/deprecations#update-to-openais-self-serve-fine-tuning)
defines eligibility restrictions and retirement dates.

General file operations, model listing, model retrieval, inference, usage records, and existing webhook records remain available.
The CLI does not reject model IDs based on their prefix. The API determines which models you can use.
Explicit machine output and extraction retain original API fields, including historical records.
Readable webhook discovery lists events supported by the CLI.
