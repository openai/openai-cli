# Changelog

## [1.10.0](https://github.com/openai/openai-cli/compare/v1.9.0...v1.10.0) (2026-09-02)


### Features

* **api:** add compute_units to Responses and Chat Completions usage ([#142](https://github.com/openai/openai-cli/issues/142)) ([fe626c6](https://github.com/openai/openai-cli/commit/fe626c6411f106422aba65bcb989db70e12a366e))
* **api:** make function call output call IDs optional ([#140](https://github.com/openai/openai-cli/issues/140)) ([0cb94ba](https://github.com/openai/openai-cli/commit/0cb94baec8f39fa26ad2bbf815c4b0aabded2213))
* **api:** update usage APIs and documentation ([#147](https://github.com/openai/openai-cli/issues/147)) ([f0c1afd](https://github.com/openai/openai-cli/commit/f0c1afddc4863eb36c2d3216a4598545147d3783))


### Chores

* **deps:** bump the codeql group across 1 directory with 2 updates ([#145](https://github.com/openai/openai-cli/issues/145)) ([5933dd5](https://github.com/openai/openai-cli/commit/5933dd50fbea96b54cb11c46221bd70ed1aa6a52))
* **deps:** update openai-go to v3.54.0 ([#139](https://github.com/openai/openai-cli/issues/139)) ([23ade14](https://github.com/openai/openai-cli/commit/23ade14892181599ea69cbb886f48494fcb56dfe))


### Documentation

* add canonical CLI security model ([#146](https://github.com/openai/openai-cli/issues/146)) ([682a6e8](https://github.com/openai/openai-cli/commit/682a6e8a7c354640a691dd77e712f06bba59748f))

## [1.9.0](https://github.com/openai/openai-cli/compare/v1.8.0...v1.9.0) (2026-08-26)


### Features

* **api:** Add residency flag and deprecate geography ([#136](https://github.com/openai/openai-cli/issues/136)) ([e460582](https://github.com/openai/openai-cli/commit/e4605829d7d63d2bb4dcc808b78b3ed76d1d0145))


### Bug Fixes

* Add provenance-aware untrusted stdin mode ([#137](https://github.com/openai/openai-cli/issues/137)) ([a133707](https://github.com/openai/openai-cli/commit/a1337079da63bfec8e4c55a009af2690a8ff8473))
* build CLI releases with patched Go toolchains ([#93](https://github.com/openai/openai-cli/issues/93)) ([a58f1cc](https://github.com/openai/openai-cli/commit/a58f1ccae5b89f61066defcab05cbd636c05f6eb))
* discard fish completion diagnostics ([#109](https://github.com/openai/openai-cli/issues/109)) ([4f35fc0](https://github.com/openai/openai-cli/commit/4f35fc038d2e75b3967fac466b9d2aa1c530427e))
* escape raw output controls on interactive terminals ([#112](https://github.com/openai/openai-cli/issues/112)) ([7601afb](https://github.com/openai/openai-cli/commit/7601afb0ac531aa40ac82e48aaa2b5dfab7a2745))
* fail closed when bootstrapping Go dependencies ([#94](https://github.com/openai/openai-cli/issues/94)) ([4ebe610](https://github.com/openai/openai-cli/commit/4ebe610bf56c63fce195f41b7146113685d85d38))
* lock and verify Steady mock tooling ([#95](https://github.com/openai/openai-cli/issues/95)) ([681d595](https://github.com/openai/openai-cli/commit/681d595d302ffe9db8ec6a7efbd32c2762785ce1))
* prepare release artifacts before publishing ([#111](https://github.com/openai/openai-cli/issues/111)) ([4193619](https://github.com/openai/openai-cli/commit/41936195a95f246ce1f45caf2600919394e2364b))
* redact sensitive response headers in debug logs ([#106](https://github.com/openai/openai-cli/issues/106)) ([4931572](https://github.com/openai/openai-cli/commit/49315720f7793cf043137d1f3fd4814698134222))
* reject invalid multipart MIME header controls ([#105](https://github.com/openai/openai-cli/issues/105)) ([fad98b4](https://github.com/openai/openai-cli/commit/fad98b4e2febb9cb6fd1c68a189ce44cb80b20d3))
* restrict downloaded file permissions ([#107](https://github.com/openai/openai-cli/issues/107)) ([7a3a7d1](https://github.com/openai/openai-cli/commit/7a3a7d15e5865497d6a1c058304a71d1d1034ef7))
* safely construct bash file completions ([#110](https://github.com/openai/openai-cli/issues/110)) ([b3e0946](https://github.com/openai/openai-cli/commit/b3e0946d9ae83ea5e31b24d728e5f7b80dd1b472))
* stream binary download responses ([#108](https://github.com/openai/openai-cli/issues/108)) ([1f3ba6c](https://github.com/openai/openai-cli/commit/1f3ba6cc8e8f9ac31744b35093e7a6be4acf2dd0))
* stream multipart uploads without buffering ([#65](https://github.com/openai/openai-cli/issues/65)) ([bcf0ecd](https://github.com/openai/openai-cli/commit/bcf0ecd42b9c3350db641abd4c56a0ece9dd51ad))
* verify GoReleaser before privileged release execution ([#96](https://github.com/openai/openai-cli/issues/96)) ([b3729ea](https://github.com/openai/openai-cli/commit/b3729ea2b4f1075d730e54f2f1be62167d55848c))


### Chores

* **api:** clarify image background and audio delta docs ([#124](https://github.com/openai/openai-cli/issues/124)) ([056f306](https://github.com/openai/openai-cli/commit/056f306419877c8b97b4e5a2340a90f6bfda717f))
* **api:** No customer-visible SDK or public API changes ([#128](https://github.com/openai/openai-cli/issues/128)) ([12e0d28](https://github.com/openai/openai-cli/commit/12e0d2891bd1f53b3536c2664e4c59797cf743e2))
* **api:** refresh image model API reference ([#115](https://github.com/openai/openai-cli/issues/115)) ([a771913](https://github.com/openai/openai-cli/commit/a7719136b8ed401b0c51a05553a5e4f720150307))
* **api:** Refresh the bundled OpenAPI reference ([#66](https://github.com/openai/openai-cli/issues/66)) ([d14387a](https://github.com/openai/openai-cli/commit/d14387a46865b8d227d3a1d607dfd40143904047))
* **api:** Report no customer-visible SDK or public API changes ([#113](https://github.com/openai/openai-cli/issues/113)) ([8da2d53](https://github.com/openai/openai-cli/commit/8da2d53491f0d04431efa89e0802d949fdb57551))
* **api:** update generation metadata only ([#138](https://github.com/openai/openai-cli/issues/138)) ([4244a62](https://github.com/openai/openai-cli/commit/4244a62d530c81ef1924ac6ef666dbab10d58e64))
* configure Dependabot for Go and GitHub Actions ([#69](https://github.com/openai/openai-cli/issues/69)) ([f495366](https://github.com/openai/openai-cli/commit/f4953669e8752ceaaf63199f891e0f36b1fbb6ae))
* **deps:** bump actions/attest-build-provenance from 4.1.0 to 4.2.2 ([#102](https://github.com/openai/openai-cli/issues/102)) ([6594c73](https://github.com/openai/openai-cli/commit/6594c73b818054ad4df62488fcc0045aab37d97f))
* **deps:** bump actions/checkout from 6.0.2 to 7.0.1 ([#103](https://github.com/openai/openai-cli/issues/103)) ([ce6a6d0](https://github.com/openai/openai-cli/commit/ce6a6d04156b97657fee60a6d3dc33fed2c312bd))
* **deps:** bump actions/checkout from 6.0.2 to 7.0.1 ([#134](https://github.com/openai/openai-cli/issues/134)) ([f40d5f2](https://github.com/openai/openai-cli/commit/f40d5f2f09afffadc0fd1062897b3c9c40c116e5))
* **deps:** bump actions/download-artifact from 4.3.0 to 8.0.1 ([#133](https://github.com/openai/openai-cli/issues/133)) ([98ed029](https://github.com/openai/openai-cli/commit/98ed029a2aa673f70eac30940fa8c3e4cb0fa1c1))
* **deps:** bump actions/github-script from 7.1.0 to 9.0.0 ([#132](https://github.com/openai/openai-cli/issues/132)) ([978ce47](https://github.com/openai/openai-cli/commit/978ce471fb40703d358eb2c3d1d6a4c6ecd78f60))
* **deps:** bump actions/setup-go in / ([#101](https://github.com/openai/openai-cli/issues/101)) ([5564982](https://github.com/openai/openai-cli/commit/55649826b7714562d85a98119e69715edb6b3bbe))
* **deps:** bump actions/upload-artifact from 4.6.2 to 7.0.1 ([#104](https://github.com/openai/openai-cli/issues/104)) ([b3ac15a](https://github.com/openai/openai-cli/commit/b3ac15a7feb4602671ea9658187ebcb52c321908))
* **deps:** bump github.com/charmbracelet/bubbles from 0.21.0 to 1.0.0 ([#99](https://github.com/openai/openai-cli/issues/99)) ([7ac7035](https://github.com/openai/openai-cli/commit/7ac70353fa01b9b6303d8957b9c346dc9188fb98))
* **deps:** bump github.com/urfave/cli/v3 from 3.4.1 to 3.10.1 in the go-minor-and-patch group across 1 directory ([#130](https://github.com/openai/openai-cli/issues/130)) ([ddc612e](https://github.com/openai/openai-cli/commit/ddc612ef94e14266a5bdb02f4434b5581ae233d5))
* **deps:** bump the charmbracelet group across 1 directory with 2 updates ([#97](https://github.com/openai/openai-cli/issues/97)) ([2137b80](https://github.com/openai/openai-cli/commit/2137b80a5a37d5b515bba6f93e888db2b0856321))
* **deps:** bump the codeql group across 1 directory with 2 updates ([#100](https://github.com/openai/openai-cli/issues/100)) ([c95ac0a](https://github.com/openai/openai-cli/commit/c95ac0aa7a3a60628a4934d7cab0aa385a5f337c))
* **deps:** bump the codeql group across 1 directory with 2 updates ([#131](https://github.com/openai/openai-cli/issues/131)) ([58e3bdd](https://github.com/openai/openai-cli/commit/58e3bdde780f3798f11ad5fbd6ed07abcf191837))
* **deps:** bump the go-minor-and-patch group across 1 directory with 5 updates ([#98](https://github.com/openai/openai-cli/issues/98)) ([74822be](https://github.com/openai/openai-cli/commit/74822be7d1e5b0a4383cd85295e3e32621469514))
* **deps:** update openai-go to v3.51.0 ([#63](https://github.com/openai/openai-cli/issues/63)) ([68e3e70](https://github.com/openai/openai-cli/commit/68e3e707ef68bebda13e638fafdb4a882ccafedc))
* **deps:** update openai-go to v3.52.0 ([#67](https://github.com/openai/openai-cli/issues/67)) ([7d87ee2](https://github.com/openai/openai-cli/commit/7d87ee207c9515e2f147ecf32aadf29f4c3d1ce9))
* set a 1,000-line custom-code budget ([#126](https://github.com/openai/openai-cli/issues/126)) ([016bfb2](https://github.com/openai/openai-cli/commit/016bfb28b10fc7d5d1d88302e1a272c23eef5a24))


### Documentation

* add secure CLI agent and contributor guidance ([#72](https://github.com/openai/openai-cli/issues/72)) ([09ec087](https://github.com/openai/openai-cli/commit/09ec0879824d2be86a1ab5b8fc39fa085ff41ee9))
* standardize CLI vulnerability disclosure policy ([#71](https://github.com/openai/openai-cli/issues/71)) ([d082a01](https://github.com/openai/openai-cli/commit/d082a010f7c6cacf407d8a1581446a7857f9f1bb))

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
