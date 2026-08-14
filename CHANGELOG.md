# Changelog

## [1.8.0](https://github.com/openai/openai-cli/compare/v1.7.1...v1.8.0) (2026-08-14)


### Features

* **api:** Add gpt-daybreak-blue-latest, gpt-daybreak-red-latest, and ([ed0e180](https://github.com/openai/openai-cli/commit/ed0e18007e860032896c7e8a2c3a1b6d6bdb20de))
* **api:** add new Responses model identifiers ([#51](https://github.com/openai/openai-cli/issues/51)) ([ed0e180](https://github.com/openai/openai-cli/commit/ed0e18007e860032896c7e8a2c3a1b6d6bdb20de))
* **api:** add WebSocket stream IDs ([#58](https://github.com/openai/openai-cli/issues/58)) ([8c85942](https://github.com/openai/openai-cli/commit/8c859427d1505e68553fbd1e0563f1312486fa93))
* **api:** add workload identity access token issued event ([#55](https://github.com/openai/openai-cli/issues/55)) ([75ff54a](https://github.com/openai/openai-cli/commit/75ff54afee9a04b3f56f57026954a3ea90fed021))
* **api:** deprecate Sora video APIs ([#57](https://github.com/openai/openai-cli/issues/57)) ([7e33190](https://github.com/openai/openai-cli/commit/7e3319056fa7479f371cf966e93b7a40a94ae6e2))
* **api:** Ultrafast tier, structured MCP and websocket errors, separate websocket events ([#62](https://github.com/openai/openai-cli/issues/62)) ([068a640](https://github.com/openai/openai-cli/commit/068a6407cc3e6b42d896c149bc4846abd815a683))


### Bug Fixes

* **api:** clarify audio upload metadata requirements ([#52](https://github.com/openai/openai-cli/issues/52)) ([17c793d](https://github.com/openai/openai-cli/commit/17c793d3c62fb938bac251f14f303f9b6f7a9421))
* **api:** handle nullable stream flags ([#53](https://github.com/openai/openai-cli/issues/53)) ([fa22e6e](https://github.com/openai/openai-cli/commit/fa22e6ee81cbfef2baf2e9c750b487224b1bdeb9))


### Chores

* **api:** Update generated-source attribution in file headers ([1b6ec73](https://github.com/openai/openai-cli/commit/1b6ec733a7033caa99d2c06ac22d37c6e75d34f8))
* **api:** Update generated-source attribution in file headers ([#49](https://github.com/openai/openai-cli/issues/49)) ([1b6ec73](https://github.com/openai/openai-cli/commit/1b6ec733a7033caa99d2c06ac22d37c6e75d34f8))
* **deps:** update openai-go to v3.50.0 ([#61](https://github.com/openai/openai-cli/issues/61)) ([f60f255](https://github.com/openai/openai-cli/commit/f60f25564e09a0e9dcbef2c0bfef855c8d2614dd))
* remove Stainless attribution and infrastructure ([#54](https://github.com/openai/openai-cli/issues/54)) ([2e59ae5](https://github.com/openai/openai-cli/commit/2e59ae57979a1b893c9f91958c9a2d2ba77714de))


### Documentation

* **api:** describe response stream event unions ([#59](https://github.com/openai/openai-cli/issues/59)) ([033612a](https://github.com/openai/openai-cli/commit/033612a28ce865d8ae193128ff707ccc42ba3fe6))

## [1.7.1](https://github.com/openai/openai-cli/compare/v1.7.0...v1.7.1) (2026-08-05)


### Bug Fixes

* **api:** add module metadata for api_reference package ([#45](https://github.com/openai/openai-cli/issues/45)) ([337a64f](https://github.com/openai/openai-cli/commit/337a64f48dca311887f8d3ee8b5a95f88d91b966))

## 1.7.0 (2026-07-31)

Full Changelog: [v1.6.0...v1.7.0](https://github.com/openai/openai-cli/compare/v1.6.0...v1.7.0)

### Features

* Add mutual TLS client certificate support ([#42](https://github.com/openai/openai-cli/issues/42)) ([d2afc76](https://github.com/openai/openai-cli/commit/d2afc7637b2432f87bd8e340b4b18eaa840e573b))
* **api:** content provenance checks ([f35f186](https://github.com/openai/openai-cli/commit/f35f1867212198f675c803caa9b6cc1c7f2593a8))
* **api:** fast tier ([be4f41e](https://github.com/openai/openai-cli/commit/be4f41e0e5dd00d4ef887a05e82e8185a845eb29))
* **api:** manual updates ([4aceee6](https://github.com/openai/openai-cli/commit/4aceee65a57ef3a4fe7807ab8de22d1a20a214f9))


### Bug Fixes

* **stlc:** stop hand-edited CI workflows from blocking seals and builds ([3157d3f](https://github.com/openai/openai-cli/commit/3157d3f27d9c054e3c231f71b0935b2724eae6ee))

## 1.6.0 (2026-07-28)

Full Changelog: [v1.5.0...v1.6.0](https://github.com/openai/openai-cli/compare/v1.5.0...v1.6.0)

### Features

* **api:** transcription model updates ([8b665dc](https://github.com/openai/openai-cli/commit/8b665dc243d4c904335f2981f3998d5d5133e39b))


### Bug Fixes

* **release:** make partially published releases retryable ([#37](https://github.com/openai/openai-cli/issues/37)) ([d43c2d7](https://github.com/openai/openai-cli/commit/d43c2d7543bc48e8ba903cd8ee57241bbd4891b5))

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
