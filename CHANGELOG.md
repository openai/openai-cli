# Changelog

## 1.5.1 (2026-07-24)

Full Changelog: [v1.5.0...v1.5.1](https://github.com/openai/openai-cli/compare/v1.5.0...v1.5.1)

### Bug Fixes

* **release:** make partially published releases retryable ([#37](https://github.com/openai/openai-cli/issues/37)) ([f823929](https://github.com/openai/openai-cli/commit/f823929757cdb536d7ad9d648a08d51d20490140))

## 1.5.0 (2026-07-24)

Full Changelog: [v1.4.0...v1.5.0](https://github.com/openai/openai-cli/compare/v1.4.0...v1.5.0)

### Features

* **api:** /organization/projects/{project_id}/service_accounts/{service_account_id}/api_keys" endpoint ([ad0f9fa](https://github.com/openai/openai-cli/commit/ad0f9fa6c506df6341cb3e7a7170cde6a6744fef))
* **api:** accept `None` for prompt_cache_key/safety_identifier ([ef9b250](https://github.com/openai/openai-cli/commit/ef9b250fcb6b054bba894e4df2d42a00ba6fb03d))
* **api:** add support for `spend_limit` admin apis ([5992345](https://github.com/openai/openai-cli/commit/5992345efb39143ef81b6d19c8f589765160f599))
* **api:** manual updates ([3c1f4b8](https://github.com/openai/openai-cli/commit/3c1f4b878c9c9e612f70369816c2fcf98306a350))
* **api:** manual updates ([cff8c9d](https://github.com/openai/openai-cli/commit/cff8c9d86782342fbef787152ac4cfa32fdd70f7))
* **stlc:** configurable CI runner and private-production-repo support in workflow templates ([8c73e8c](https://github.com/openai/openai-cli/commit/8c73e8c92716a1cdb9a374f1325626828a5b9577))


### Chores

* **internal:** codegen related update ([6a272cd](https://github.com/openai/openai-cli/commit/6a272cd43849f14518f43fc62b5aec0658830124))
* **internal:** codegen related update ([5a04ac8](https://github.com/openai/openai-cli/commit/5a04ac8f304f05ea7fee9046fc10969fbc10756b))

## 1.4.0 (2026-07-14)

Full Changelog: [v1.3.0...v1.4.0](https://github.com/openai/openai-cli/compare/v1.3.0...v1.4.0)

### Features

* **api:** add owner_project_access to APIKeyListParams ([571c96f](https://github.com/openai/openai-cli/commit/571c96f4cf676a45e35451a29a7636468e59716d))
* **api:** gpt-5.6-sol updates ([977bac7](https://github.com/openai/openai-cli/commit/977bac746ae0b7dfc46c4cb4043e64de32d42cf5))


### Chores

* **internal:** codegen related update ([337b800](https://github.com/openai/openai-cli/commit/337b80062039cedaac772fec6aa089da93c26e14))
* **internal:** codegen related update ([57caf1e](https://github.com/openai/openai-cli/commit/57caf1eaf22f37257b314ca7f6094c0a814a7415))

## 1.3.0 (2026-06-17)

Full Changelog: [v1.2.0...v1.3.0](https://github.com/openai/openai-cli/compare/v1.2.0...v1.3.0)

### Features

* **api:** admin spend_alerts ([f183cb0](https://github.com/openai/openai-cli/commit/f183cb04db80e2a89364c0b46b81f355edbeced5))
* **api:** responses.moderation and chat_completions.moderation ([d660d08](https://github.com/openai/openai-cli/commit/d660d0873624f3895d9463ec98e6afc9d63a52b8))
* **api:** update OpenAPI spec or Stainless config ([7aefee4](https://github.com/openai/openai-cli/commit/7aefee4982a088a9299dc68953f0dea796ee5ff5))
* **api:** update OpenAPI spec or Stainless config ([dee1f3e](https://github.com/openai/openai-cli/commit/dee1f3ea2e7dee54aaa44faddd27675d7e44e43d))

## 1.2.0 (2026-06-01)

Full Changelog: [v1.1.2...v1.2.0](https://github.com/openai/openai-cli/compare/v1.1.2...v1.2.0)

### Features

* **api:** add project permissions, file-search/web-search usage, service_tier param ([d4dbcea](https://github.com/openai/openai-cli/commit/d4dbcea2238a04169e248ca95af635970db577c3))
* **api:** add service_tier parameter to responses compact method ([5901109](https://github.com/openai/openai-cli/commit/5901109e1b3b10b3802fcd8581a9c4e4a5658fc9))
* **api:** manual updates ([e7d9ba0](https://github.com/openai/openai-cli/commit/e7d9ba0465032c5fc2863af7713ae0bc38e87546))
* **api:** update OpenAPI spec or Stainless config ([d33292f](https://github.com/openai/openai-cli/commit/d33292f1dbba1f2dc4a2f846f06e7f0561a24a65))
* **api:** workload identity in audit logs, additional_tools item in responses, fix ActionSearch.query to be optional. ([c6ad190](https://github.com/openai/openai-cli/commit/c6ad1900935bde057461767e8758455b66cc91ab))


### Bug Fixes

* **cli:** apply generated API update ([1d789ad](https://github.com/openai/openai-cli/commit/1d789ad7835030ddd943cec6b31c4f7c30eb303c))
* treat text/plan with format: binary as raw upload ([68b3d40](https://github.com/openai/openai-cli/commit/68b3d407dd9bc659e86f44e919e155043ba9e9bb))


### Chores

* **api:** bump go sdk version to 3.38.0 for CLI ([8081ea0](https://github.com/openai/openai-cli/commit/8081ea0d92fba3cd23f65f3b8f5ef1fed8c2e92d))


### Documentation

* update README examples ([7e43c6a](https://github.com/openai/openai-cli/commit/7e43c6ad73326fa697a9d1c092bf5886bb0ee0d4))

## 1.1.2 (2026-05-07)

Full Changelog: [v1.1.1...v1.1.2](https://github.com/openai/openai-cli/compare/v1.1.1...v1.1.2)

## 1.1.1 (2026-05-07)

Full Changelog: [v1.1.0...v1.1.1](https://github.com/openai/openai-cli/compare/v1.1.0...v1.1.1)

## 1.1.0 (2026-05-07)

Full Changelog: [v1.0.0...v1.1.0](https://github.com/openai/openai-cli/compare/v1.0.0...v1.1.0)

### Features

* **api:** realtime 2 ([c83180c](https://github.com/openai/openai-cli/commit/c83180caca0349714da4625796e2af2a1f152345))


### Bug Fixes

* **api:** fix imagegen `size` enum regression ([6ae6672](https://github.com/openai/openai-cli/commit/6ae6672cea74af3147412af49a1155f954eb7a41))


### Chores

* redact api-key headers in debug logs ([7452ba1](https://github.com/openai/openai-cli/commit/7452ba1b6d4ad0ddf62ebe2c3f34f88a23b1c6d8))


### Documentation

* **api:** update top-logprobs description in chat completions and responses ([f3e204e](https://github.com/openai/openai-cli/commit/f3e204e5d95a0bc347ff21ed59ee5261b7bc38f6))

## 1.0.0 (2026-05-05)

Full Changelog: [v1.0.0...v1.0.0](https://github.com/openai/openai-cli/compare/v1.0.0...v1.0.0)

### Features

* **api:** launch realtime translate + update image 2 ([18f5119](https://github.com/openai/openai-cli/commit/18f5119393e51b8651988bc996aea905c4170d4c))
* **api:** manual updates ([82b2da2](https://github.com/openai/openai-cli/commit/82b2da24861d08d2ec5596cd344f30f34b62900d))

## 1.0.0 (2026-05-04)

Full Changelog: [v0.0.1...v1.0.0](https://github.com/openai/openai-cli/compare/v0.0.1...v1.0.0)

### Features

* **api:** add params, update optionality in admin org projects/users ([41ae0ee](https://github.com/openai/openai-cli/commit/41ae0ee9a64f9c2966ef58db16f8b15531060d7a))


### Bug Fixes

* **api:** unpin go SDK version for CLI ([b33c0dd](https://github.com/openai/openai-cli/commit/b33c0dd7628d2ca856a61b1cdffea192cb546c27))

## 0.0.1 (2026-05-01)

Initial release
