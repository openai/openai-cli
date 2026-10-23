# Changelog

## [1.40.4](https://github.com/openai/openai-cli/compare/v1.40.3...v1.40.4) (2026-10-23)


### Documentation

* complete 1.40.3 release notes ([#588](https://github.com/openai/openai-cli/issues/588)) ([494b628](https://github.com/openai/openai-cli/commit/494b628c3ef50853dc6b6554139bfec544f9a21a))

## [1.40.3](https://github.com/openai/openai-cli/compare/v1.40.2...v1.40.3) (2026-10-22)


### Features

* **audio:** improve speech playback, seeking, history, and saving. Add opt-in live microphone and browser-tab translation. ([#575](https://github.com/openai/openai-cli/pull/575)) ([7cd714d](https://github.com/openai/openai-cli/commit/7cd714d74c83bccdd07b8982912a85485a786d0a))
* **explore:** add local command search with `openai explore` and optional shell prompt insertion. ([#580](https://github.com/openai/openai-cli/pull/580)) ([a740376](https://github.com/openai/openai-cli/commit/a74037654d66266e566a7a34730c27f7e5e1419c))
* **images:** keep generation forms, progress, and saved images in a persistent terminal workspace. Retained gallery metadata uses version 2; older binaries need a fresh Terminal tab with the normal text font. ([#574](https://github.com/openai/openai-cli/pull/574)) ([b868ab4](https://github.com/openai/openai-cli/commit/b868ab40449d99e5ad336d5d47900969eafbfaba))
* **updates:** show quiet update notices during eligible interactive startup. Updates remain manual. ([#581](https://github.com/openai/openai-cli/pull/581)) ([46f130b](https://github.com/openai/openai-cli/commit/46f130b4aac0183c7623f56f9958bc9bbfc2cc36))
* **webhooks:** add guided endpoint creation, endpoint inspection, and delivery recovery guidance. ([#404](https://github.com/openai/openai-cli/pull/404)) ([16f624b](https://github.com/openai/openai-cli/commit/16f624b4f22535f9559ad1249926b9da6b7568d5))


### Chores

* **agents:** prepare the session runtime. This adds no public Agents commands or workspace UI. ([#396](https://github.com/openai/openai-cli/pull/396)) ([70c7452](https://github.com/openai/openai-cli/commit/70c745204954412f83b230e0b54d8ef253be1ba7))
* **deps:** update openai-go to v3.85.1 ([#584](https://github.com/openai/openai-cli/issues/584)) ([44f4156](https://github.com/openai/openai-cli/commit/44f41561b93f67f9f3a84c6896d6644e474ed221))


### Documentation

* clarify CLI workflows and refresh the README ([#585](https://github.com/openai/openai-cli/issues/585)) ([145e67c](https://github.com/openai/openai-cli/commit/145e67cf47cbe17a738989fe1c9526f4fd44eaa8))

## [1.40.2](https://github.com/openai/openai-cli/compare/v1.40.1...v1.40.2) (2026-10-22)


### Chores

* **api:** clarify service-account API key deletion ([#582](https://github.com/openai/openai-cli/issues/582)) ([7d32d04](https://github.com/openai/openai-cli/commit/7d32d0447842a4e664c1835a01ae94d7530e5e04))

## [1.40.1](https://github.com/openai/openai-cli/compare/v1.40.0...v1.40.1) (2026-10-21)


### Bug Fixes

* **completion:** show recovery guidance when WSL shortcut setup or removal cannot safely update shell files ([#497](https://github.com/openai/openai-cli/issues/497)) ([c73035a](https://github.com/openai/openai-cli/commit/c73035a0da29bfdb718801a3646715a38094219d))


### Chores

* **deps:** update openai-go to v3.85.0 ([#576](https://github.com/openai/openai-cli/issues/576)) ([442d8ae](https://github.com/openai/openai-cli/commit/442d8aede60889508b80549487d7fcdbc8a88bf2))

## [1.40.0](https://github.com/openai/openai-cli/compare/v1.39.1...v1.40.0) (2026-10-21)


### Features

* **api:** add custom voice consent management ([#572](https://github.com/openai/openai-cli/issues/572)) ([7080124](https://github.com/openai/openai-cli/commit/708012431f8e4f64d20489e290690d6ed8ecdc30))


### Chores

* **api:** clarify cache-write token totals ([#570](https://github.com/openai/openai-cli/issues/570)) ([bd86692](https://github.com/openai/openai-cli/commit/bd866925a3fcf0dc6877067678f37df445ffd60a))

## [1.39.1](https://github.com/openai/openai-cli/compare/v1.39.0...v1.39.1) (2026-10-21)


### Bug Fixes

* build CLI releases with Go 1.26.10 ([#571](https://github.com/openai/openai-cli/issues/571)) ([1097d74](https://github.com/openai/openai-cli/commit/1097d74755b36efc3892b36eec69a7a2d0f485e0))
* explain missing file-write permission in readable errors ([#550](https://github.com/openai/openai-cli/issues/550)) ([03b1ccd](https://github.com/openai/openai-cli/commit/03b1ccd2f0c56526f82254db32e3f327183c9859))
* normalize automatic download filenames ([#556](https://github.com/openai/openai-cli/issues/556)) ([4deb0c1](https://github.com/openai/openai-cli/commit/4deb0c12cd05d949bc62508f61c2822c63dbf05a))
* preserve literal PowerShell reference completions ([#554](https://github.com/openai/openai-cli/issues/554)) ([d39c80b](https://github.com/openai/openai-cli/commit/d39c80bb049b9e9ba55ba02830dbfd10c07cffbd))
* reject symlinks as explicit download destinations; use the target path directly ([e539d97](https://github.com/openai/openai-cli/commit/e539d9772a0db679dc2c5c1f77fec4df556c9980))
* require HTTPS for remote API endpoints ([#549](https://github.com/openai/openai-cli/issues/549)) ([2f2232a](https://github.com/openai/openai-cli/commit/2f2232a2f72b8ad44b148017b171ce34af97e984))
* use visible generated names for automatic hidden filename suggestions ([fa7f917](https://github.com/openai/openai-cli/commit/fa7f917e8a872cda9ac4174ec437b495ccd522c2))


### Chores

* **api:** describe 30-minute cache-write usage counts ([#551](https://github.com/openai/openai-cli/issues/551)) ([e963b2f](https://github.com/openai/openai-cli/commit/e963b2f5b9d493313061e09273c6ab62ece426cd))
* **deps:** bump actions/cache from 4.3.0 to 6.1.0 ([#544](https://github.com/openai/openai-cli/issues/544)) ([34a9790](https://github.com/openai/openai-cli/commit/34a9790b32b0f8e8d614928af3bf1cb2491a2ee8))
* **deps:** bump actions/download-artifact from 8.0.1 to 8.0.2 ([#545](https://github.com/openai/openai-cli/issues/545)) ([fb273e3](https://github.com/openai/openai-cli/commit/fb273e352638108229c00b78aae8b04400947230))
* **deps:** bump actions/upload-artifact from 7.0.1 to 7.0.2 ([#542](https://github.com/openai/openai-cli/issues/542)) ([31ea038](https://github.com/openai/openai-cli/commit/31ea0385d1b7c73edaa0a7065da697c066c587f7))
* **deps:** update openai-go to v3.84.0 ([#569](https://github.com/openai/openai-cli/issues/569)) ([74fe949](https://github.com/openai/openai-cli/commit/74fe949c464fec5bf759ae256d3336eaba47a23b))
* finish generated tooling layout ([#566](https://github.com/openai/openai-cli/issues/566)) ([9e47d2e](https://github.com/openai/openai-cli/commit/9e47d2e4a125f21c354f7f64988a024fa87ec95a))
* refresh API-key constraints and Castiron reporting ([#564](https://github.com/openai/openai-cli/issues/564)) ([9983bef](https://github.com/openai/openai-cli/commit/9983befc63f4bf263e6b20ca92fd7888c2ccce7d))


### Refactors

* **admin:** isolate credential and secure setup workflows ([#553](https://github.com/openai/openai-cli/issues/553)) ([44fa4f3](https://github.com/openai/openai-cli/commit/44fa4f3ff76f8aabb69244aba3cfe21188636f38))
* **app:** consolidate handwritten application runtime ([#563](https://github.com/openai/openai-cli/issues/563)) ([7639dab](https://github.com/openai/openai-cli/commit/7639dab16a033ef6335cbb386b990b2ff6098d80))
* **app:** move help assembly out of custom ([#561](https://github.com/openai/openai-cli/issues/561)) ([7e8300a](https://github.com/openai/openai-cli/commit/7e8300adcef5ccabe13574f9c818401147ff9686))
* **app:** own optional first-run setup admission ([#562](https://github.com/openai/openai-cli/issues/562)) ([4a159a6](https://github.com/openai/openai-cli/commit/4a159a67b74818d51d16c97f48e17d7cda7e432a))
* **audio:** isolate speech and local audio workflows ([#557](https://github.com/openai/openai-cli/issues/557)) ([f5558be](https://github.com/openai/openai-cli/commit/f5558be09ec4e1ee9a5c9b83cfad0b59c31fc057))
* **images:** isolate image workflows and presentation ([#560](https://github.com/openai/openai-cli/issues/560)) ([6ec6708](https://github.com/openai/openai-cli/commit/6ec670807e4d9b81d5c58690901c80e121822806))
* **models:** separate workflows and shared list navigation ([#548](https://github.com/openai/openai-cli/issues/548)) ([82d5341](https://github.com/openai/openai-cli/commit/82d5341af6df0aa5c8e277c8d41457ec97edf8ca))
* remove legacy runtime adapter ([#565](https://github.com/openai/openai-cli/issues/565)) ([04e59e0](https://github.com/openai/openai-cli/commit/04e59e00fcceb6ad1145f0b739448403127c1c2c))

## [1.39.0](https://github.com/openai/openai-cli/compare/v1.38.0...v1.39.0) (2026-10-19)


### Features

* **admin:** add guided admin-key setup and access verification ([#387](https://github.com/openai/openai-cli/issues/387)) ([4183995](https://github.com/openai/openai-cli/commit/418399574848f9bb894d660d84ece12f78f9b092))
* **api:** add Decisions creation and hosted environment reference ([#437](https://github.com/openai/openai-cli/issues/437)) ([0ca3ac7](https://github.com/openai/openai-cli/commit/0ca3ac7021f88de81f7442d299093814afcf45c2))
* **api:** add Decisions prompt caching options ([#501](https://github.com/openai/openai-cli/issues/501)) ([5031be2](https://github.com/openai/openai-cli/commit/5031be2adc789a4e2e96b51f0a682a93097aaf8f))
* **api:** add organization Decisions usage reporting ([#435](https://github.com/openai/openai-cli/issues/435)) ([ba6cb09](https://github.com/openai/openai-cli/commit/ba6cb091387803581722e99443d8f3bfc59f9326))
* **api:** add prompt-based voice creation ([#502](https://github.com/openai/openai-cli/issues/502)) ([72f552b](https://github.com/openai/openai-cli/commit/72f552bbfe38d5fe3f018ef1257db59b51319120))
* **api:** add safety identifiers to image requests and responses ([#503](https://github.com/openai/openai-cli/issues/503)) ([b624c9f](https://github.com/openai/openai-cli/commit/b624c9ff262f2201d2b1523601c0c24bd628fa1d))
* **api:** configure Decisions when accepting Live SIP calls ([#505](https://github.com/openai/openai-cli/issues/505)) ([e17ac83](https://github.com/openai/openai-cli/commit/e17ac832f7201ad1d0c7a3905ccace79e1b33ba8))
* **api:** support project API key scope updates ([#478](https://github.com/openai/openai-cli/issues/478)) ([7afecce](https://github.com/openai/openai-cli/commit/7afeccea67647dfa0031fc80a7f29520a3f01efa))
* **api:** support updating project API key expiry ([#462](https://github.com/openai/openai-cli/issues/462)) ([97b43f2](https://github.com/openai/openai-cli/commit/97b43f2cacc586ab352de1d544bd0b3c89936364))
* **audio:** add speech workflows, local history, playback, export, and readable transcripts ([#403](https://github.com/openai/openai-cli/issues/403)) ([fe17bcb](https://github.com/openai/openai-cli/commit/fe17bcbc30cd30dc4ab525e7036ea0ef0ddc5f3b))
* **batches:** add waiting, result collection, and safe downloads ([#388](https://github.com/openai/openai-cli/issues/388)) ([ecaf876](https://github.com/openai/openai-cli/commit/ecaf876d034ea37c8c1f01087c9c000c660a0df3))
* **cli:** add scoped error recovery and optional shell typo suggestions ([#409](https://github.com/openai/openai-cli/issues/409)) ([a81ec27](https://github.com/openai/openai-cli/commit/a81ec27645fccd8187a6ac61453e31e80bf335d5))
* **cli:** show a welcome card during bare interactive startup ([#401](https://github.com/openai/openai-cli/issues/401)) ([024dbaa](https://github.com/openai/openai-cli/commit/024dbaa695b5f10289dded3dcab5db1af0620bdd))
* **files:** add get and download routes with positional uploads ([#397](https://github.com/openai/openai-cli/issues/397)) ([e689398](https://github.com/openai/openai-cli/commit/e68939820415144d769ed02de6aa72d5b7d32948))
* **help:** show complete help consistently and improve flag diagnostics ([#384](https://github.com/openai/openai-cli/issues/384)) ([6fcc478](https://github.com/openai/openai-cli/commit/6fcc478086507571286727084a7d7b162a37ab51))
* **images:** add interactive and opt-in Konsole previews ([#371](https://github.com/openai/openai-cli/issues/371)) ([d3bc819](https://github.com/openai/openai-cli/commit/d3bc819041757108d39222b239bfe427c421fd40))
* **images:** retain picker drafts and keep failed requests editable ([#394](https://github.com/openai/openai-cli/issues/394)) ([1dc9d3d](https://github.com/openai/openai-cli/commit/1dc9d3d2a18f2a9ad016f539d337f5544e665ac2))
* **models:** add filtered ID and owner lists with interactive navigation ([#391](https://github.com/openai/openai-cli/issues/391)) ([86502b0](https://github.com/openai/openai-cli/commit/86502b0d5b614155746daedb6a435e209fdb443e))
* **output:** add quiet and verbose controls ([#392](https://github.com/openai/openai-cli/issues/392)) ([866d877](https://github.com/openai/openai-cli/commit/866d8770c00c331bf3630b05f2a76870110d2896))
* **tokenizer:** add offline token inspection and local Codex guidance ([#386](https://github.com/openai/openai-cli/issues/386)) ([217ff2e](https://github.com/openai/openai-cli/commit/217ff2ea1e853040ba08c3c170e05586424386aa))


### Bug Fixes

* **autocomplete:** preserve colon argument boundaries ([#413](https://github.com/openai/openai-cli/issues/413)) ([5739ce0](https://github.com/openai/openai-cli/commit/5739ce0f3a6cc5887092554a403bf53357eca9a4))
* **autocomplete:** preserve quoted and Unicode filenames during Bash completion ([#446](https://github.com/openai/openai-cli/issues/446)) ([6ec7646](https://github.com/openai/openai-cli/commit/6ec7646e76e1934ef4b10701b56cd79949028d45))
* **autocomplete:** preserve quoted Bash command prefixes ([#399](https://github.com/openai/openai-cli/issues/399)) ([c3c66c4](https://github.com/openai/openai-cli/commit/c3c66c4c014e7759a6f3b61a595d1c54fd542db9))
* **autocomplete:** restore Fish 3 completion and literal filename prefixes ([#447](https://github.com/openai/openai-cli/issues/447)) ([f93670c](https://github.com/openai/openai-cli/commit/f93670c039148eb812ce0ac256a4e84694ce3034))
* **ci:** decode SDK cross-link events as UTF-8 ([#457](https://github.com/openai/openai-cli/issues/457)) ([af0e8a0](https://github.com/openai/openai-cli/commit/af0e8a00d6060ab5d4004449ab306966f9366334))
* **cli:** emit finite JSON arrays; use --format jsonl for independent records ([#407](https://github.com/openai/openai-cli/issues/407)) ([b93e41e](https://github.com/openai/openai-cli/commit/b93e41e12201e806d86c3d69fc6e3c7c3bf44768))
* **cli:** wait briefly for Windows piped input ([#485](https://github.com/openai/openai-cli/issues/485)) ([8b40840](https://github.com/openai/openai-cli/commit/8b40840535e17d32b3bd4d3fd5defe657e30b0d2))
* **errors:** preserve incomplete resumed-stream failures and empty API error details ([#439](https://github.com/openai/openai-cli/issues/439)) ([e8ffaab](https://github.com/openai/openai-cli/commit/e8ffaab16604f6a4ef22b1cb6549a0b44283cbbe))
* **help:** preserve literal flag values and quoted Bash file completion ([#440](https://github.com/openai/openai-cli/issues/440)) ([47d6a16](https://github.com/openai/openai-cli/commit/47d6a16f909342f87af4a7c66a6b3ab96d429f4e))
* **images:** preserve reserved probe directories during Windows image saves ([#470](https://github.com/openai/openai-cli/issues/470)) ([e330cc2](https://github.com/openai/openai-cli/commit/e330cc2997ae3d7220afca0a09c94e712c47ceb3))
* **images:** retain saved-file recovery guidance after cancellation ([#442](https://github.com/openai/openai-cli/issues/442)) ([337a7e4](https://github.com/openai/openai-cli/commit/337a7e448aed26b454f63a13469a19689053a7a6))
* **io:** preserve explicit stdin, stream events promptly, and protect incomplete downloads ([#395](https://github.com/openai/openai-cli/issues/395)) ([390a9c3](https://github.com/openai/openai-cli/commit/390a9c3d68e93f7348509ffec169bb5ab38d68cc))
* **models:** finish SIGINT shutdown with status 130 ([#441](https://github.com/openai/openai-cli/issues/441)) ([673c5eb](https://github.com/openai/openai-cli/commit/673c5ebab5ed5f2c51d954b75983121be25e2d16))
* **output:** keep redirected results undecorated and preserve stream cleanup ([#392](https://github.com/openai/openai-cli/issues/392)) ([866d877](https://github.com/openai/openai-cli/commit/866d8770c00c331bf3630b05f2a76870110d2896))
* **output:** send successful API save receipts to stderr ([#395](https://github.com/openai/openai-cli/issues/395)) ([390a9c3](https://github.com/openai/openai-cli/commit/390a9c3d68e93f7348509ffec169bb5ab38d68cc))
* **tokenizer:** preserve Windows Unicode input and split terminal replies ([#454](https://github.com/openai/openai-cli/issues/454)) ([911218a](https://github.com/openai/openai-cli/commit/911218a71df84ae14e79c43a9a9174cc2f14a7bd))


### Chores

* **api:** clarify Decisions request and response fields ([#476](https://github.com/openai/openai-cli/issues/476)) ([8301197](https://github.com/openai/openai-cli/commit/83011971aa627069f86c919ed610dbb07ba6b50d))
* **api:** correct the shida Live voice name ([#430](https://github.com/openai/openai-cli/issues/430)) ([193ee5b](https://github.com/openai/openai-cli/commit/193ee5b56e304723fa1995ff96aa711c876b45ef))
* **api:** document 128-character safety identifiers ([#428](https://github.com/openai/openai-cli/issues/428)) ([a6bf1af](https://github.com/openai/openai-cli/commit/a6bf1af6633fa4d49d8adf5283343b5814134f65))
* **api:** document beta pagination validation and migration ([#431](https://github.com/openai/openai-cli/issues/431)) ([729045e](https://github.com/openai/openai-cli/commit/729045ee4042582b3878e74c42e449b3b7252ec3))
* **api:** document beta session spending controls ([#432](https://github.com/openai/openai-cli/issues/432)) ([47df976](https://github.com/openai/openai-cli/commit/47df9766d29c970082e6b50b90c86d12c8657d19))
* **api:** document billing_not_active session errors ([#429](https://github.com/openai/openai-cli/issues/429)) ([8f26fe4](https://github.com/openai/openai-cli/commit/8f26fe41d00ab021d63195fa6f9561349ff2bf39))
* **api:** document detailed safety alert explanations ([#434](https://github.com/openai/openai-cli/issues/434)) ([2b61ced](https://github.com/openai/openai-cli/commit/2b61cedea6934da4d42ddedfdbffd8e8f32e834c))
* **api:** document Files API rate-limit responses ([#427](https://github.com/openai/openai-cli/issues/427)) ([88cd309](https://github.com/openai/openai-cli/commit/88cd309619677d90528c23af8b8871edd219a8c0))
* **api:** document image tool error and moderation details ([#451](https://github.com/openai/openai-cli/issues/451)) ([53b3b6e](https://github.com/openai/openai-cli/commit/53b3b6e8ca28ee0ac25e35109366e8d979174787))
* **api:** document Live audio fixture events in the API reference ([#481](https://github.com/openai/openai-cli/issues/481)) ([38c82ce](https://github.com/openai/openai-cli/commit/38c82ce7d1b9d2fda7f88255b6b20f66935fc02f))
* **api:** document OCI external-storage providers ([#433](https://github.com/openai/openai-cli/issues/433)) ([4f1f7df](https://github.com/openai/openai-cli/commit/4f1f7df4dac9c434ede4a4cade820a5a69b9df46))
* **api:** document Realtime response error messages ([#479](https://github.com/openai/openai-cli/issues/479)) ([ec8d114](https://github.com/openai/openai-cli/commit/ec8d114f92d02a6deb91b6731eb9ca396f4f9c78))
* **api:** document regional processing residency values ([#477](https://github.com/openai/openai-cli/issues/477)) ([75865b6](https://github.com/openai/openai-cli/commit/75865b6e163a030c2d636c63647cf9f84eeae081))
* **api:** document snapshot lifecycle status values and events ([#415](https://github.com/openai/openai-cli/issues/415)) ([53828f0](https://github.com/openai/openai-cli/commit/53828f030b5b74f5ca3da44ba341865228827ffb))
* **api:** document vault updates and metadata filters ([#426](https://github.com/openai/openai-cli/issues/426)) ([b724518](https://github.com/openai/openai-cli/commit/b724518c69b00cb70c0c3487ed021392b606b5b0))
* **api:** use neutral moderation examples ([#518](https://github.com/openai/openai-cli/issues/518)) ([180544a](https://github.com/openai/openai-cli/commit/180544a5eb341b0f4bc93fda821c4c863a78d7f3))
* clarify Live limits and refresh generated-file verification ([#510](https://github.com/openai/openai-cli/issues/510)) ([9cddd6b](https://github.com/openai/openai-cli/commit/9cddd6bc8715eb06ec37176328cc6e3148d9e827))
* **deps:** bump charm.land/bubbletea/v2 from 2.0.10 to 2.1.0 in the go-minor-and-patch group across 1 directory ([#539](https://github.com/openai/openai-cli/issues/539)) ([37d5e5c](https://github.com/openai/openai-cli/commit/37d5e5c348bb68573da8d2112622e6310710fbce))
* **deps:** bump the codeql group across 1 directory with 2 updates ([#543](https://github.com/openai/openai-cli/issues/543)) ([9023b9e](https://github.com/openai/openai-cli/commit/9023b9e9c202aaf0eb6f0d1d3d7d1c4112570fb7))
* **deps:** bump the go-minor-and-patch group across 1 directory with 5 updates ([#444](https://github.com/openai/openai-cli/issues/444)) ([263dadd](https://github.com/openai/openai-cli/commit/263dadd23530f55da776b944b559e5c6642e6f25))
* **deps:** bump the golang-x group across 1 directory with 2 updates ([#540](https://github.com/openai/openai-cli/issues/540)) ([52f0ad0](https://github.com/openai/openai-cli/commit/52f0ad031c79d287a1e896fb27f84b6a61da7ceb))
* **deps:** update openai-go to v3.74.0 ([#393](https://github.com/openai/openai-cli/issues/393)) ([e32158e](https://github.com/openai/openai-cli/commit/e32158e57f05eae260ebb7a0bd0cacc6b2961ce1))
* **deps:** update openai-go to v3.75.0 ([#412](https://github.com/openai/openai-cli/issues/412)) ([077ae69](https://github.com/openai/openai-cli/commit/077ae69604621bf0c6814c7c66c8a6906177c06d))
* **deps:** update openai-go to v3.77.0 ([#414](https://github.com/openai/openai-cli/issues/414)) ([835de59](https://github.com/openai/openai-cli/commit/835de5951f37122e4c04ce613b94e38800f5ca3c))
* **deps:** update openai-go to v3.78.0 ([#450](https://github.com/openai/openai-cli/issues/450)) ([8f79e98](https://github.com/openai/openai-cli/commit/8f79e984b87eaa7808bebedc428881f025c78ce2))
* **deps:** update openai-go to v3.79.0 ([#507](https://github.com/openai/openai-cli/issues/507)) ([0828c79](https://github.com/openai/openai-cli/commit/0828c79cbca6447ce9719f4e6f447d2d6db5e295))
* **deps:** update openai-go to v3.81.0 ([#516](https://github.com/openai/openai-cli/issues/516)) ([6c26343](https://github.com/openai/openai-cli/commit/6c2634386e5829ad321c0be761a12d8a6050fb07))
* **deps:** update openai-go to v3.82.0 ([#532](https://github.com/openai/openai-cli/issues/532)) ([00add07](https://github.com/openai/openai-cli/commit/00add0789246208983379c2a03b6679ee336f31d))
* restore generated reporting tools without behavior changes ([#475](https://github.com/openai/openai-cli/issues/475)) ([c12cd56](https://github.com/openai/openai-cli/commit/c12cd56cc5ecf67e958654fbb122204071ce8363))


### Documentation

* define CLI layout migration and compatibility contract ([#486](https://github.com/openai/openai-cli/issues/486)) ([e9ad31e](https://github.com/openai/openai-cli/commit/e9ad31e68acfa588c873549a92e5b40be6979118))
* **windows:** explain JSON stderr capture in Windows PowerShell 5.1 ([#443](https://github.com/openai/openai-cli/issues/443)) ([960af77](https://github.com/openai/openai-cli/commit/960af77801394e5f0e3eed64090b03c0fec21571))


### Refactors

* **batches:** separate local workflows from assembly policy ([#547](https://github.com/openai/openai-cli/issues/547)) ([0486923](https://github.com/openai/openai-cli/commit/048692309d1cb5334b06f8dd5bb8f76191325332))
* extract CLI invocation runner into internal app ([#488](https://github.com/openai/openai-cli/issues/488)) ([ee7a731](https://github.com/openai/openai-cli/commit/ee7a731b91ade984da59ff307a138196087d5dc0))
* **files:** separate handwritten workflows and shared shell helpers ([#546](https://github.com/openai/openai-cli/issues/546)) ([a6bfb0b](https://github.com/openai/openai-cli/commit/a6bfb0b3676a860b94bb81fe1de42311ec3dfc10))
* **input:** move non-consuming piped input probes ([#529](https://github.com/openai/openai-cli/issues/529)) ([904c95d](https://github.com/openai/openai-cli/commit/904c95d3693f4220af3680216960fae6a491670c))
* isolate browser platform handling in internal/terminal ([#504](https://github.com/openai/openai-cli/issues/504)) ([9d304ca](https://github.com/openai/openai-cli/commit/9d304cae6bd3631c9f88e0f2854393f88f74f996))
* isolate command-tree organization ([#515](https://github.com/openai/openai-cli/issues/515)) ([b5cc7a8](https://github.com/openai/openai-cli/commit/b5cc7a8f94adbc869beae9b92421a4e6c66b4d83))
* isolate generated tokenizer tables ([#496](https://github.com/openai/openai-cli/issues/496)) ([efda414](https://github.com/openai/openai-cli/commit/efda4149c76e1093c656d10feb69b4959fbf7a78))
* isolate the Codex command feature ([#509](https://github.com/openai/openai-cli/issues/509)) ([a6530ae](https://github.com/openai/openai-cli/commit/a6530ae24054949a080aecbcbdb453a8a428a6f7))
* isolate the manpage command feature ([#514](https://github.com/openai/openai-cli/issues/514)) ([7617d3d](https://github.com/openai/openai-cli/commit/7617d3da4fa9b575ad4bd4119a40b7e12b731463))
* isolate the shared terminal output runtime ([#512](https://github.com/openai/openai-cli/issues/512)) ([d93d0d0](https://github.com/openai/openai-cli/commit/d93d0d06f646830c8aa879b5ba7f22d6bcb8d09e))
* isolate the tokenizer command feature ([#513](https://github.com/openai/openai-cli/issues/513)) ([fb023ee](https://github.com/openai/openai-cli/commit/fb023ee0f21e52f1a9ece71d050d311dc8d9a6c1))
* keep mTLS transport construction in internal/request ([#522](https://github.com/openai/openai-cli/issues/522)) ([393e1ae](https://github.com/openai/openai-cli/commit/393e1ae727da0bcb8c0665201a341f59d0e4fc68))
* keep request-input help with its renderer ([#519](https://github.com/openai/openai-cli/issues/519)) ([5791763](https://github.com/openai/openai-cli/commit/57917633684e5054e479e3af82a3cd5746d935b4))
* **lists:** add shared paged-list navigation runtime ([#389](https://github.com/openai/openai-cli/issues/389)) ([d866707](https://github.com/openai/openai-cli/commit/d86670735891cb7cb1c948573430d3df4641c237))
* **lists:** prepare compact table rendering for resource lists ([#390](https://github.com/openai/openai-cli/issues/390)) ([c65e03e](https://github.com/openai/openai-cli/commit/c65e03e16f537faf85d2522c31b431a4ac1c550a))
* move command recovery into help ([#517](https://github.com/openai/openai-cli/issues/517)) ([5662bc2](https://github.com/openai/openai-cli/commit/5662bc2567e85a40d8fc65493245fb0d6af5e86f))
* move response transformations into internal output ([#487](https://github.com/openai/openai-cli/issues/487)) ([55ebd62](https://github.com/openai/openai-cli/commit/55ebd62813ec2a70e7c98ec52dd030cc0d632227))
* **output:** move finite JSON list framing and paging ([#537](https://github.com/openai/openai-cli/issues/537)) ([ee77628](https://github.com/openai/openai-cli/commit/ee776284218917e7c83740e401415b5a71947cb5))
* **output:** move lazy transformation iterator ([#535](https://github.com/openai/openai-cli/issues/535)) ([ad22022](https://github.com/openai/openai-cli/commit/ad22022fbab927f11cbcf9a8cab9a4b0c39e2d97))
* **output:** move pager and stdout streaming runtime ([#536](https://github.com/openai/openai-cli/issues/536)) ([cb5a5a8](https://github.com/openai/openai-cli/commit/cb5a5a8c951caaff2afa2b0547c386b0d6588b2b))
* **output:** move readable list and stream rendering ([#538](https://github.com/openai/openai-cli/issues/538)) ([8b304b0](https://github.com/openai/openai-cli/commit/8b304b05d8b8f0246c47808cfa0e6a29f5432a1c))
* **output:** move result formatting and format selection ([#534](https://github.com/openai/openai-cli/issues/534)) ([50db1df](https://github.com/openai/openai-cli/commit/50db1dfcb7d01017406230e8ca46f442c3a87746))
* **request:** extract piped input reading and merge policy ([#528](https://github.com/openai/openai-cli/issues/528)) ([068bddc](https://github.com/openai/openai-cli/commit/068bddc2270a15f593f5c3ccdd50d8a57c98db1d))
* **request:** move default client options and URL validation ([#530](https://github.com/openai/openai-cli/issues/530)) ([211f056](https://github.com/openai/openai-cli/commit/211f056f36aa3f11f2856a307f6f44ddddf4d215))
* **request:** move file expansion and single-use stdin ([#526](https://github.com/openai/openai-cli/issues/526)) ([8be3c0b](https://github.com/openai/openai-cli/commit/8be3c0baf2bb27ff0104b684c9688b4ce6a0b69c))
* **request:** move multipart encoding and owned uploads ([#524](https://github.com/openai/openai-cli/issues/524)) ([73f9a1f](https://github.com/openai/openai-cli/commit/73f9a1f95d576c619131697b420a7de658ba0ef6))
* **request:** move shared flag and stdin preparation ([#533](https://github.com/openai/openai-cli/issues/533)) ([8e64575](https://github.com/openai/openai-cli/commit/8e645751d1611a0d4617aff80caa12672977754a))
* **request:** move stdin provenance and input policies ([#525](https://github.com/openai/openai-cli/issues/525)) ([ac5aa07](https://github.com/openai/openai-cli/commit/ac5aa07b84e0ca9b7f03995aff469bd2f2655eeb))
* **request:** move streaming mode resolution ([#527](https://github.com/openai/openai-cli/issues/527)) ([64ae997](https://github.com/openai/openai-cli/commit/64ae997d5a4d3b110fa7f0a5928ea52824add74c))
* **request:** separate final encoding from feature hooks ([#531](https://github.com/openai/openai-cli/issues/531)) ([711596c](https://github.com/openai/openai-cli/commit/711596c43b4d8b3a7180e5707e630d972cdd1091))
* separate generated API commands from handwritten CLI code ([#500](https://github.com/openai/openai-cli/issues/500)) ([d44fb85](https://github.com/openai/openai-cli/commit/d44fb85fdc1cdfeb4897529dfbbca2c38d6bd703))
* separate generated commands from CLI compatibility behavior ([#474](https://github.com/openai/openai-cli/issues/474)) ([08df3f6](https://github.com/openai/openai-cli/commit/08df3f68eb3518f6abffe94d6edb5f5a5e9d66db))
* share literal request headers through internal/request ([#521](https://github.com/openai/openai-cli/issues/521)) ([a9f35b9](https://github.com/openai/openai-cli/commit/a9f35b97127cbf2e0eff857d489bc69907c2f564))
* share output writing and error classification ([#506](https://github.com/openai/openai-cli/issues/506)) ([bce2ab8](https://github.com/openai/openai-cli/commit/bce2ab8087d413d08aee21e16439274b4b741c53))
* share root request flags and mTLS configuration ([#523](https://github.com/openai/openai-cli/issues/523)) ([a718ab6](https://github.com/openai/openai-cli/commit/a718ab6bec846f9bee23104d199a3732080a4b3e))
* share terminal color and presentation policy ([#508](https://github.com/openai/openai-cli/issues/508)) ([575d494](https://github.com/openai/openai-cli/commit/575d4945b35df20db9f548a6b212a09c23f8dae3))

## [1.38.0](https://github.com/openai/openai-cli/compare/v1.37.0...v1.38.0) (2026-10-08)


### Features

* **api:** add usage source and user costs grouping ([#376](https://github.com/openai/openai-cli/issues/376)) ([17b9b60](https://github.com/openai/openai-cli/commit/17b9b60b6feae1f4c42df7ce444c0e40d3b377a6))
* **cli:** improve command groups, add task shortcuts, and fix shell completion ([reload completion](https://github.com/openai/openai-cli/blob/dce0616da8e33c879359e6538827989334bc99b0/docs/shell-completion.md)) ([dce0616](https://github.com/openai/openai-cli/commit/dce0616da8e33c879359e6538827989334bc99b0))
* **images:** add native previews for Warp ([#377](https://github.com/openai/openai-cli/issues/377)) ([e433bd2](https://github.com/openai/openai-cli/commit/e433bd26bdc85d31046f32604fa370ef23a54dcc))
* **images:** add opt-in VS Code PNG previews with `OPENAI_VSCODE_IMAGES=1` ([setup](https://github.com/openai/openai-cli/blob/3540c75acd6f1dd9f09684dfd9b30cb51aae2dc4/docs/image-generation-saving.md#inline-previews)) ([3540c75](https://github.com/openai/openai-cli/commit/3540c75acd6f1dd9f09684dfd9b30cb51aae2dc4))
* **images:** show common picker settings together and keep the multiline command preview visible ([e6d3f50](https://github.com/openai/openai-cli/commit/e6d3f50b92af3ae55cab7ff069d10bcfea7b5ecf))


### Bug Fixes

* **api:** correct custom voice creation parameters ([#385](https://github.com/openai/openai-cli/issues/385)) ([d32b3ae](https://github.com/openai/openai-cli/commit/d32b3ae1d3fbf2445feca289454279407271a983))
* **cli:** allow global request flags throughout commands and fix environment precedence and completion setup ([da762ff](https://github.com/openai/openai-cli/commit/da762ffff4f35732f4720ac4db531d8d764f2cbe))
* **images:** align picker borders and refine empty prompts ([#370](https://github.com/openai/openai-cli/issues/370)) ([4cd297a](https://github.com/openai/openai-cli/commit/4cd297a1c8ce002afb4e2c0e4854d34e2315ad29))
* **images:** prevent encoded image output after Ctrl-C in Kitty on macOS ([60603c5](https://github.com/openai/openai-cli/commit/60603c545087920cb9f4b68232c77f9b188f5e0d))


### Chores

* **deps:** bump the codeql group across 1 directory with 2 updates ([#374](https://github.com/openai/openai-cli/issues/374)) ([8f70d2a](https://github.com/openai/openai-cli/commit/8f70d2abfb914bcb75c91814eda241ad41c41239))
* **deps:** bump the go-minor-and-patch group across 1 directory with 2 updates ([#373](https://github.com/openai/openai-cli/issues/373)) ([4f808f2](https://github.com/openai/openai-cli/commit/4f808f222df78e33868a7afb038defc55968a53c))
* **deps:** update openai-go to v3.71.2 ([#375](https://github.com/openai/openai-cli/issues/375)) ([2fff26b](https://github.com/openai/openai-cli/commit/2fff26b68fd33ba77564091dafe7a123d3d58063))
* **deps:** update openai-go to v3.72.0 ([#378](https://github.com/openai/openai-cli/issues/378)) ([82cdd6e](https://github.com/openai/openai-cli/commit/82cdd6ea61fc2c3c602376f7ad98f88ae1d44efa))
* **deps:** update openai-go to v3.73.0 ([#380](https://github.com/openai/openai-cli/issues/380)) ([02307cb](https://github.com/openai/openai-cli/commit/02307cb47f804fcf09996e8a57b2bbfb629d4b72))

## [1.37.0](https://github.com/openai/openai-cli/compare/v1.36.0...v1.37.0) (2026-10-04)


### Features

* **cli:** include fish picker shortcuts in Linux packages ([#361](https://github.com/openai/openai-cli/issues/361)) ([bae10cc](https://github.com/openai/openai-cli/commit/bae10ccb79bde2bacb142c387e7b31539913313a))


### Documentation

* **cli:** keep Homebrew picker setup on first run ([#367](https://github.com/openai/openai-cli/issues/367)) ([fbe7e46](https://github.com/openai/openai-cli/commit/fbe7e46715bd3a8b735bded78d351205e469bf36))

## [1.36.0](https://github.com/openai/openai-cli/compare/v1.35.0...v1.36.0) (2026-10-03)


### Features

* **cli:** save picker shortcuts for future shell sessions ([#360](https://github.com/openai/openai-cli/issues/360)) ([814316e](https://github.com/openai/openai-cli/commit/814316eee2eda76a623130b58626535bd7df3cf9))

## [1.35.0](https://github.com/openai/openai-cli/compare/v1.34.0...v1.35.0) (2026-10-03)


### Features

* **images:** open the picker with Tab in supported shells ([#359](https://github.com/openai/openai-cli/issues/359)) ([8b6db1f](https://github.com/openai/openai-cli/commit/8b6db1f4ac85b0cae997de379e1951954a204683))

## [1.34.0](https://github.com/openai/openai-cli/compare/v1.33.0...v1.34.0) (2026-10-03)


### Features

* **images:** remember picker settings and save folder ([#358](https://github.com/openai/openai-cli/issues/358)) ([1081066](https://github.com/openai/openai-cli/commit/1081066c5caedcf4f1e235256558e936bbd28082))

## [1.33.0](https://github.com/openai/openai-cli/compare/v1.32.1...v1.33.0) (2026-10-03)


### Features

* **images:** add an inline generation picker ([#357](https://github.com/openai/openai-cli/issues/357)) ([100628d](https://github.com/openai/openai-cli/commit/100628d8490075870954c4988f2016e30d019368))

## [1.32.1](https://github.com/openai/openai-cli/compare/v1.32.0...v1.32.1) (2026-10-02)


### Chores

* **deps:** update openai-go to v3.71.1 ([#346](https://github.com/openai/openai-cli/issues/346)) ([7083dc6](https://github.com/openai/openai-cli/commit/7083dc66693766939fbf74ea5b15ac60d6312e45))

## [1.32.0](https://github.com/openai/openai-cli/compare/v1.31.1...v1.32.0) (2026-10-02)


### Features

* **api:** add custom voice creation command ([#352](https://github.com/openai/openai-cli/issues/352)) ([805f102](https://github.com/openai/openai-cli/commit/805f10213c26a8ee6a72fdc2c5d49a62f42981eb))

## [1.31.1](https://github.com/openai/openai-cli/compare/v1.31.0...v1.31.1) (2026-10-01)


### Bug Fixes

* **demos:** remove subgroup exit-code footer ([#349](https://github.com/openai/openai-cli/issues/349)) ([8f2ac67](https://github.com/openai/openai-cli/commit/8f2ac6786039e2c6a5f599ec5080a224db189675))

## [1.31.0](https://github.com/openai/openai-cli/compare/v1.30.7...v1.31.0) (2026-10-01)


### Features

* add browsable command subgroups ([#347](https://github.com/openai/openai-cli/issues/347)) ([1805191](https://github.com/openai/openai-cli/commit/1805191f797953a23b373c6ffd6f41895deba92d))

## [1.30.7](https://github.com/openai/openai-cli/compare/v1.30.6...v1.30.7) (2026-09-30)


### Chores

* **api:** document original image detail in Chat Completions ([#343](https://github.com/openai/openai-cli/issues/343)) ([0a10600](https://github.com/openai/openai-cli/commit/0a1060062e8867343a1f0445474815de45c43026))

## [1.30.6](https://github.com/openai/openai-cli/compare/v1.30.5...v1.30.6) (2026-09-30)


### Bug Fixes

* **debug:** limit response header values to operational metadata ([#339](https://github.com/openai/openai-cli/issues/339)) ([6609abe](https://github.com/openai/openai-cli/commit/6609abe59f496532b213e51d175d1e508374c829))
* omit query data from debug request logs ([#338](https://github.com/openai/openai-cli/issues/338)) ([4603381](https://github.com/openai/openai-cli/commit/4603381ba4987c3ce906419d1d4208648a24338e))


### Chores

* **api:** correct the eval run cancellation endpoint reference ([#341](https://github.com/openai/openai-cli/issues/341)) ([331dd2b](https://github.com/openai/openai-cli/commit/331dd2be05a8b7e6975f99eb49627c780de6a3b7))
* **api:** refresh API reference and preserve Live session commands ([#337](https://github.com/openai/openai-cli/issues/337)) ([cabdceb](https://github.com/openai/openai-cli/commit/cabdceb7c74cd6c8e65c763b9762794683b6ffab))

## [1.30.5](https://github.com/openai/openai-cli/compare/v1.30.4...v1.30.5) (2026-09-30)


### Chores

* **api:** correct grader example and SIP auth reference ([#334](https://github.com/openai/openai-cli/issues/334)) ([41917c6](https://github.com/openai/openai-cli/commit/41917c68e2890464d1d56d4781b937ded6cc17b5))

## [1.30.4](https://github.com/openai/openai-cli/compare/v1.30.3...v1.30.4) (2026-09-30)


### Chores

* **api:** refresh API reference and preserve Live session commands ([#332](https://github.com/openai/openai-cli/issues/332)) ([099b47d](https://github.com/openai/openai-cli/commit/099b47d3961ac20df2c675a8bfa7aa266a8f7d50))

## [1.30.3](https://github.com/openai/openai-cli/compare/v1.30.2...v1.30.3) (2026-09-29)


### Chores

* **api:** refresh SDK metadata ([#330](https://github.com/openai/openai-cli/issues/330)) ([ce4a72b](https://github.com/openai/openai-cli/commit/ce4a72b935814f9c3976a89bddea9d7ee867f998))

## [1.30.2](https://github.com/openai/openai-cli/compare/v1.30.1...v1.30.2) (2026-09-29)


### Chores

* **deps:** update openai-go to v3.68.0 ([#327](https://github.com/openai/openai-cli/issues/327)) ([838fc91](https://github.com/openai/openai-cli/commit/838fc91184e85cb4ad442096a68f6e92c054652d))

## [1.30.1](https://github.com/openai/openai-cli/compare/v1.30.0...v1.30.1) (2026-09-29)


### Chores

* **api:** retain the existing command surface ([#326](https://github.com/openai/openai-cli/issues/326)) ([22f1255](https://github.com/openai/openai-cli/commit/22f12559fff1531228298d165fa2e039c48a95d6))

## [1.30.0](https://github.com/openai/openai-cli/compare/v1.29.3...v1.30.0) (2026-09-29)


### Features

* **cli:** show loading feedback for image requests ([#318](https://github.com/openai/openai-cli/issues/318)) ([51436e3](https://github.com/openai/openai-cli/commit/51436e3b95e203fc772f5320fce4981f8f6a27de))

## [1.29.3](https://github.com/openai/openai-cli/compare/v1.29.2...v1.29.3) (2026-09-29)


### Chores

* **api:** add GPT-6.1 Sol to bundled specification ([#322](https://github.com/openai/openai-cli/issues/322)) ([0ec8a85](https://github.com/openai/openai-cli/commit/0ec8a856a31ec8bd93f32eedaf47d8127af096e2))

## [1.29.2](https://github.com/openai/openai-cli/compare/v1.29.1...v1.29.2) (2026-09-29)


### Bug Fixes

* **cli:** show common image commands first ([#317](https://github.com/openai/openai-cli/issues/317)) ([f676145](https://github.com/openai/openai-cli/commit/f676145cbf9613a52e00ca74f413300e1160b117))

## [1.29.1](https://github.com/openai/openai-cli/compare/v1.29.0...v1.29.1) (2026-09-29)


### Documentation

* **cli:** explain image settings and saving defaults ([#316](https://github.com/openai/openai-cli/issues/316)) ([e544427](https://github.com/openai/openai-cli/commit/e544427250eb148d179f83741489f34d9a7bec9e))

## [1.29.0](https://github.com/openai/openai-cli/compare/v1.28.2...v1.29.0) (2026-09-28)


### Features

* **cli:** clearer help examples and full reference ([#315](https://github.com/openai/openai-cli/issues/315)) ([096e437](https://github.com/openai/openai-cli/commit/096e437482e3d56933e95138f58579956b4c5600))

## [1.28.2](https://github.com/openai/openai-cli/compare/v1.28.1...v1.28.2) (2026-09-28)


### Chores

* **deps:** bump github.com/charmbracelet/x/ansi from 0.11.7 to 0.11.8 in the charmbracelet group across 1 directory ([#308](https://github.com/openai/openai-cli/issues/308)) ([724df7e](https://github.com/openai/openai-cli/commit/724df7e20e0f8a9d4b0aa28493d2097c6138b97b))
* **deps:** bump golang.org/x/image from 0.45.0 to 0.46.0 in the golang-x group across 1 directory ([#309](https://github.com/openai/openai-cli/issues/309)) ([c27db9c](https://github.com/openai/openai-cli/commit/c27db9cd8ab92774344141a8807d840fcf578570))
* **deps:** bump the codeql group across 1 directory with 2 updates ([#311](https://github.com/openai/openai-cli/issues/311)) ([7ec00c0](https://github.com/openai/openai-cli/commit/7ec00c0eeb3ba537a9b9590e6aab2e824d0c5ccb))
* **deps:** bump the go-minor-and-patch group across 1 directory with 4 updates ([#310](https://github.com/openai/openai-cli/issues/310)) ([6b9ba37](https://github.com/openai/openai-cli/commit/6b9ba3782e3538baaf35002cd95f4c7a930acbc4))

## [1.28.1](https://github.com/openai/openai-cli/compare/v1.28.0...v1.28.1) (2026-09-28)


### Bug Fixes

* **api:** clarify file downloads and Agents API options ([#305](https://github.com/openai/openai-cli/issues/305)) ([e54ed76](https://github.com/openai/openai-cli/commit/e54ed7693a42f5fbafec4eb3c32dd49093c15152))

## [1.28.0](https://github.com/openai/openai-cli/compare/v1.27.0...v1.28.0) (2026-09-27)


### Features

* **images:** show progress previews while saving ([#302](https://github.com/openai/openai-cli/issues/302)) ([19f85a1](https://github.com/openai/openai-cli/commit/19f85a117e8ee198f139dd19e329e08cd3d248a9))

## [1.27.0](https://github.com/openai/openai-cli/compare/v1.26.0...v1.27.0) (2026-09-26)


### Features

* preview saved images and remember inline settings ([#297](https://github.com/openai/openai-cli/issues/297)) ([53518d7](https://github.com/openai/openai-cli/commit/53518d783e9b0a3cc79ea107956f609fa63972c0))

## [1.26.0](https://github.com/openai/openai-cli/compare/v1.25.2...v1.26.0) (2026-09-26)


### Features

* preview saved images in the terminal ([#296](https://github.com/openai/openai-cli/issues/296)) ([5c069fa](https://github.com/openai/openai-cli/commit/5c069fa600563c16829c5005438fee1fb36ddc0b))

## [1.25.2](https://github.com/openai/openai-cli/compare/v1.25.1...v1.25.2) (2026-09-26)


### Bug Fixes

* **completion:** hide internal commands without arguments ([#299](https://github.com/openai/openai-cli/issues/299)) ([f5a62e8](https://github.com/openai/openai-cli/commit/f5a62e8c45c222a605eee9097f3b5715a6b804ca))

## [1.25.1](https://github.com/openai/openai-cli/compare/v1.25.0...v1.25.1) (2026-09-26)


### Bug Fixes

* **jsonview:** retain the selected row when toggling raw JSON ([#218](https://github.com/openai/openai-cli/issues/218)) ([f003d12](https://github.com/openai/openai-cli/commit/f003d1216b7a246d630fb1dcce1a8617effb0ed7))

## [1.25.0](https://github.com/openai/openai-cli/compare/v1.24.0...v1.25.0) (2026-09-26)


### Features

* save image edits and variations ([#294](https://github.com/openai/openai-cli/issues/294)) ([5213c3d](https://github.com/openai/openai-cli/commit/5213c3d3cb76492903773d12f8a26b99181f6943))

## [1.24.0](https://github.com/openai/openai-cli/compare/v1.23.0...v1.24.0) (2026-09-26)


### Features

* save generated images with defaults ([#291](https://github.com/openai/openai-cli/issues/291)) ([0e7ec75](https://github.com/openai/openai-cli/commit/0e7ec75a121c12f1c260f9224c4eb4429635bbd5))

## [1.23.0](https://github.com/openai/openai-cli/compare/v1.22.0...v1.23.0) (2026-09-25)


### Features

* **cli:** show readable audio results and transcript streams ([#289](https://github.com/openai/openai-cli/issues/289)) ([111173a](https://github.com/openai/openai-cli/commit/111173a6a5f2b70372402f23a90ca728b2e8d747))

## [1.22.0](https://github.com/openai/openai-cli/compare/v1.21.0...v1.22.0) (2026-09-25)


### Features

* **cli:** stream readable response text ([#279](https://github.com/openai/openai-cli/issues/279)) ([08ed41e](https://github.com/openai/openai-cli/commit/08ed41ee132f969986debe716f0ab0872f579a5e))


### Chores

* **api:** clarify documented API error responses ([#287](https://github.com/openai/openai-cli/issues/287)) ([c6a5af5](https://github.com/openai/openai-cli/commit/c6a5af52883bee58f6cd401b41e230f150b9007e))

## [1.21.0](https://github.com/openai/openai-cli/compare/v1.20.4...v1.21.0) (2026-09-25)


### Features

* **cli:** show focused list and retrieve results ([#278](https://github.com/openai/openai-cli/issues/278)) ([4a16021](https://github.com/openai/openai-cli/commit/4a160213f95f7c8eaa46217f234bbdcbe1cc197e))

## [1.20.4](https://github.com/openai/openai-cli/compare/v1.20.3...v1.20.4) (2026-09-25)


### Chores

* **api:** document fine-tuning and model errors ([#284](https://github.com/openai/openai-cli/issues/284)) ([ed97a81](https://github.com/openai/openai-cli/commit/ed97a81dc85125ce8efc735e25773a47c11529f1))

## [1.20.3](https://github.com/openai/openai-cli/compare/v1.20.2...v1.20.3) (2026-09-25)


### Chores

* **api:** document stored chat completion errors ([#282](https://github.com/openai/openai-cli/issues/282)) ([1287876](https://github.com/openai/openai-cli/commit/128787625657632fae69a191dfe4a373c5e10e75))

## [1.20.2](https://github.com/openai/openai-cli/compare/v1.20.1...v1.20.2) (2026-09-25)


### Bug Fixes

* reuse pending Homebrew cask update PR ([#280](https://github.com/openai/openai-cli/issues/280)) ([da8b100](https://github.com/openai/openai-cli/commit/da8b1002abd6abea84269d535ebc958e1b5b4bdd))

## [1.20.1](https://github.com/openai/openai-cli/compare/v1.20.0...v1.20.1) (2026-09-25)


### Bug Fixes

* **cmd:** split PAGER into program and arguments ([#208](https://github.com/openai/openai-cli/issues/208)) ([d3ce908](https://github.com/openai/openai-cli/commit/d3ce908972df2c5d17aea8470b3500cd90494c10))
* **jsonview:** reflow text when the terminal width changes ([#217](https://github.com/openai/openai-cli/issues/217)) ([ff05317](https://github.com/openai/openai-cli/commit/ff05317def9f595de6dd7b81790dcd9d0b7141c3))


### Chores

* **api:** document batch error responses ([#275](https://github.com/openai/openai-cli/issues/275)) ([ebcd64e](https://github.com/openai/openai-cli/commit/ebcd64eb96eb2cac972b6ae12542861b7c07cd2b))

## [1.20.0](https://github.com/openai/openai-cli/compare/v1.19.2...v1.20.0) (2026-09-25)


### Features

* **cli:** make errors easier to understand ([#266](https://github.com/openai/openai-cli/issues/266)) ([37fe587](https://github.com/openai/openai-cli/commit/37fe587ddf9e9c160f8d6c52e5f2b879f5623960))

## [1.19.2](https://github.com/openai/openai-cli/compare/v1.19.1...v1.19.2) (2026-09-25)


### Chores

* **api:** document files and uploads error responses ([#271](https://github.com/openai/openai-cli/issues/271)) ([2d394b4](https://github.com/openai/openai-cli/commit/2d394b4580a36c6b34b294b9837b3e05c12109ae))

## [1.19.1](https://github.com/openai/openai-cli/compare/v1.19.0...v1.19.1) (2026-09-25)


### Chores

* **api:** document Responses not-found errors ([#269](https://github.com/openai/openai-cli/issues/269)) ([f17c5ab](https://github.com/openai/openai-cli/commit/f17c5abf7bc11578fe8f5ca594ecd81643ce3738))

## [1.19.0](https://github.com/openai/openai-cli/compare/v1.18.2...v1.19.0) (2026-09-25)


### Features

* **cli:** make command results readable by default ([#265](https://github.com/openai/openai-cli/issues/265)) ([9d87b92](https://github.com/openai/openai-cli/commit/9d87b92bb9df3e665426cd6e5305de95a63f7cd5))

## [1.18.2](https://github.com/openai/openai-cli/compare/v1.18.1...v1.18.2) (2026-09-24)


### Bug Fixes

* suppress misleading "Did you mean" suggestions ([#17](https://github.com/openai/openai-cli/issues/17)) ([3cb6f72](https://github.com/openai/openai-cli/commit/3cb6f7254a296ba268259415d2a68dc393c56182))

## [1.18.1](https://github.com/openai/openai-cli/compare/v1.18.0...v1.18.1) (2026-09-24)


### Bug Fixes

* **api:** make transcription language flags independently addressable ([#260](https://github.com/openai/openai-cli/issues/260)) ([95b2a72](https://github.com/openai/openai-cli/commit/95b2a72568b6f4c4b84d6b6710d969539f19c3e3))
* **cli:** sanitize automatic terminal download streams ([#262](https://github.com/openai/openai-cli/issues/262)) ([f4f1cec](https://github.com/openai/openai-cli/commit/f4f1cec5d5b530cf8845b1604bc43c03847d8877))

## [1.18.0](https://github.com/openai/openai-cli/compare/v1.17.0...v1.18.0) (2026-09-24)


### Features

* **cli:** simplify help and API key setup guidance ([#246](https://github.com/openai/openai-cli/issues/246)) ([eeb5f08](https://github.com/openai/openai-cli/commit/eeb5f0811923892bd86041b3071e853ce8f2495e))


### Bug Fixes

* **output:** return immediately for zero max-items ([#220](https://github.com/openai/openai-cli/issues/220)) ([9d90763](https://github.com/openai/openai-cli/commit/9d9076315350a7453ecbd7224d627bebf7805aec))

## [1.17.0](https://github.com/openai/openai-cli/compare/v1.16.12...v1.17.0) (2026-09-24)


### Features

* **api:** add Cyber access programs to Responses ([#258](https://github.com/openai/openai-cli/issues/258)) ([eb3a800](https://github.com/openai/openai-cli/commit/eb3a800ae5d75315a33424f25f98cf8cfe4a33c1))


### Chores

* **api:** clarify Realtime modality array definitions ([#256](https://github.com/openai/openai-cli/issues/256)) ([e8a8337](https://github.com/openai/openai-cli/commit/e8a83372902121423aa2099bf8c6e3e9c6bae8d1))

## [1.16.12](https://github.com/openai/openai-cli/compare/v1.16.11...v1.16.12) (2026-09-23)


### Chores

* **api:** clarify approximate web search location defaults ([#254](https://github.com/openai/openai-cli/issues/254)) ([8996866](https://github.com/openai/openai-cli/commit/8996866be2544d99a8a447d31aa3ada48ed24fea))

## [1.16.11](https://github.com/openai/openai-cli/compare/v1.16.10...v1.16.11) (2026-09-23)


### Bug Fixes

* **autocomplete:** respect cursor position in zsh completion ([#231](https://github.com/openai/openai-cli/issues/231)) ([d04139a](https://github.com/openai/openai-cli/commit/d04139a9faa1697bcf5dab41d3f57b3274173088))

## [1.16.10](https://github.com/openai/openai-cli/compare/v1.16.9...v1.16.10) (2026-09-23)


### Bug Fixes

* **completion:** resolve command aliases while traversing commands ([#221](https://github.com/openai/openai-cli/issues/221)) ([8c43b32](https://github.com/openai/openai-cli/commit/8c43b328961356f950ed1c9f2da9726916ac03f0))
* **jsonview:** propagate lazy-loading failures ([#219](https://github.com/openai/openai-cli/issues/219)) ([ef82331](https://github.com/openai/openai-cli/commit/ef82331c2557390f9d24818f05e5b7f4b87b7ea7))

## [1.16.9](https://github.com/openai/openai-cli/compare/v1.16.8...v1.16.9) (2026-09-23)


### Bug Fixes

* **cmd:** expand file references in nullable string flags ([#216](https://github.com/openai/openai-cli/issues/216)) ([9b46ed6](https://github.com/openai/openai-cli/commit/9b46ed60c9bc5affc2e2aec4df41e14347cfebcb))

## [1.16.8](https://github.com/openai/openai-cli/compare/v1.16.7...v1.16.8) (2026-09-23)


### Bug Fixes

* **requestflag:** merge inner flags into untyped array literals ([#211](https://github.com/openai/openai-cli/issues/211)) ([808fb45](https://github.com/openai/openai-cli/commit/808fb45a8cd3797d7171bb2b0758bca01ef10b01))

## [1.16.7](https://github.com/openai/openai-cli/compare/v1.16.6...v1.16.7) (2026-09-23)


### Bug Fixes

* **jsonview:** keep single-column stream tables column-shaped ([#207](https://github.com/openai/openai-cli/issues/207)) ([64c5f23](https://github.com/openai/openai-cli/commit/64c5f23479ac43638cb91d3f9beb72063ceb3ea7))

## [1.16.6](https://github.com/openai/openai-cli/compare/v1.16.5...v1.16.6) (2026-09-23)


### Bug Fixes

* **cli:** reject non-finite float flag values ([#200](https://github.com/openai/openai-cli/issues/200)) ([c8dd3e9](https://github.com/openai/openai-cli/commit/c8dd3e90a1a0cc60abab60823902bcb50f1c86d0))

## [1.16.5](https://github.com/openai/openai-cli/compare/v1.16.4...v1.16.5) (2026-09-23)


### Chores

* **deps:** update openai-go to v3.66.0 ([#241](https://github.com/openai/openai-cli/issues/241)) ([6cc2737](https://github.com/openai/openai-cli/commit/6cc27376fcf4e4634cc109f1dd0f9ffe62a4ed2c))


### Documentation

* clarify collaborator-only pull request policy ([#242](https://github.com/openai/openai-cli/issues/242)) ([a5d5d0c](https://github.com/openai/openai-cli/commit/a5d5d0c8eb402f7d7133a8b7eff5f9931fd51d59))

## [1.16.4](https://github.com/openai/openai-cli/compare/v1.16.3...v1.16.4) (2026-09-23)


### Chores

* **api:** clarify Chat Completions seed range ([#239](https://github.com/openai/openai-cli/issues/239)) ([fcda650](https://github.com/openai/openai-cli/commit/fcda65015bd46032a328d3590f8c76f13ba6251d))

## [1.16.3](https://github.com/openai/openai-cli/compare/v1.16.2...v1.16.3) (2026-09-22)


### Chores

* **api:** Refresh API metadata ([#237](https://github.com/openai/openai-cli/issues/237)) ([03c94ac](https://github.com/openai/openai-cli/commit/03c94ac020c9c7bf9e8b26bdb89d7afe8715c14a))
* **deps:** update openai-go to v3.65.0 ([#233](https://github.com/openai/openai-cli/issues/233)) ([e90f9d9](https://github.com/openai/openai-cli/commit/e90f9d9b399946ea1bd7be10a02b476ff43163e5))

## [1.16.2](https://github.com/openai/openai-cli/compare/v1.16.1...v1.16.2) (2026-09-22)


### Chores

* **api:** document GPT-Rosalind research model ([#234](https://github.com/openai/openai-cli/issues/234)) ([8a9731c](https://github.com/openai/openai-cli/commit/8a9731ce4f5056561bc9256614b57adc610e6ce6))

## [1.16.1](https://github.com/openai/openai-cli/compare/v1.16.0...v1.16.1) (2026-09-22)


### Chores

* **deps:** update openai-go to v3.64.3 ([#230](https://github.com/openai/openai-cli/issues/230)) ([4c1280e](https://github.com/openai/openai-cli/commit/4c1280ea3924d5c6801d99494b52c547698bc543))

## [1.16.0](https://github.com/openai/openai-cli/compare/v1.15.0...v1.16.0) (2026-09-22)


### Features

* **api:** add external storage and safety case commands ([#229](https://github.com/openai/openai-cli/issues/229)) ([c30c961](https://github.com/openai/openai-cli/commit/c30c961703d7b96ac5abf8c5d464558beb6d0c3a))
* **api:** add prompt-cache prewarming ([#196](https://github.com/openai/openai-cli/issues/196)) ([9032f58](https://github.com/openai/openai-cli/commit/9032f58da73ddc63113d8fc66280f4a931bca6b1))
* **api:** add response transformation hooks ([#223](https://github.com/openai/openai-cli/issues/223)) ([8de34a7](https://github.com/openai/openai-cli/commit/8de34a7e953e3694b472e64445ccaf6c16edd8d7))
* **api:** add webhook endpoint management ([#201](https://github.com/openai/openai-cli/issues/201)) ([dea465a](https://github.com/openai/openai-cli/commit/dea465aa6f38c50552800acf7ecb839d46878063))
* **cli:** support custom request headers ([#190](https://github.com/openai/openai-cli/issues/190)) ([07e3ed9](https://github.com/openai/openai-cli/commit/07e3ed90ad4d285808490811a5638d9384fd76c5))


### Bug Fixes

* **apiform:** preserve float32 precision in comma arrays ([#78](https://github.com/openai/openai-cli/issues/78)) ([774612f](https://github.com/openai/openai-cli/commit/774612fbe896c83b2d33fe7244d2ec00470f62ad))
* **apiform:** strip path components from upload filenames ([#120](https://github.com/openai/openai-cli/issues/120)) ([a92e143](https://github.com/openai/openai-cli/commit/a92e143d5a80d09c87738da50561f3780d0e9276))
* **apiform:** support primitive pointers in comma arrays ([#119](https://github.com/openai/openai-cli/issues/119)) ([9d146cd](https://github.com/openai/openai-cli/commit/9d146cd3dfd56fec12156b8621ebe581bd4bbe62))
* **apiquery:** preserve narrow numeric parameters ([#76](https://github.com/openai/openai-cli/issues/76)) ([b152780](https://github.com/openai/openai-cli/commit/b15278003c411c0975f1769033f14cd1a3a0dc2a))
* **apiquery:** reject complex elements in comma arrays ([#118](https://github.com/openai/openai-cli/issues/118)) ([46faf2c](https://github.com/openai/openai-cli/commit/46faf2c779a9e04e7d91c94fd3444ec2a7a09783))
* **apiquery:** reject non-string map keys ([#88](https://github.com/openai/openai-cli/issues/88)) ([687a097](https://github.com/openai/openai-cli/commit/687a0975eb5f3c87c6d02633b4666ac8dac8ed33))
* **autocomplete:** omit hidden flags from suggestions ([#122](https://github.com/openai/openai-cli/issues/122)) ([137d59b](https://github.com/openai/openai-cli/commit/137d59b65e50d532f2f395fe0c5e1982c3510dfe))
* **cmd:** include backslash in path detection for @ file references ([#38](https://github.com/openai/openai-cli/issues/38)) ([f984eb1](https://github.com/openai/openai-cli/commit/f984eb1df5620cdd5dc2b8a03d892ed38cc9d015))
* **debug:** redact sensitive response headers ([#32](https://github.com/openai/openai-cli/issues/32)) ([ee62f85](https://github.com/openai/openai-cli/commit/ee62f85cc6fe0ee31775c77fb34c422185b783ac))
* **explore:** avoid panic when printing an empty result set ([#68](https://github.com/openai/openai-cli/issues/68)) ([f74838c](https://github.com/openai/openai-cli/commit/f74838cc9a66a19c01bb43bbb698e942e6a0f6ba))
* install Linux package binaries under /usr/bin ([#34](https://github.com/openai/openai-cli/issues/34)) ([ac4e7cb](https://github.com/openai/openai-cli/commit/ac4e7cb16c9271b712c2cde5f64a0904c8815c4e))
* **jsonview:** avoid width underflow in static string rendering ([#117](https://github.com/openai/openai-cli/issues/117)) ([cbdf265](https://github.com/openai/openai-cli/commit/cbdf26510cac060627aab463dde1cf0058d1c687))
* **jsonview:** preserve literal object keys in pretty output and explorer ([#143](https://github.com/openai/openai-cli/issues/143)) ([25664bf](https://github.com/openai/openai-cli/commit/25664bffc86ac956e69b4f4fd9f96ec28baf93ca))
* omit redirect destinations from multipart upload errors ([#189](https://github.com/openai/openai-cli/issues/189)) ([642d511](https://github.com/openai/openai-cli/commit/642d5119227b54a84de0b0aa38d99d148a51148c))
* **output:** stop pagination at max items ([#43](https://github.com/openai/openai-cli/issues/43)) ([552840e](https://github.com/openai/openai-cli/commit/552840e37ec4b0156ec498159e3bf03804ee643d))
* **requestflag:** preserve JSON numbers written in exponent form ([#195](https://github.com/openai/openai-cli/issues/195)) ([20db45e](https://github.com/openai/openai-cli/commit/20db45ee515e2f7f1d47f634a1b5699e20b548ac))


### Chores

* **api:** clarify Live SIP call help ([#193](https://github.com/openai/openai-cli/issues/193)) ([79435c4](https://github.com/openai/openai-cli/commit/79435c4b30f3739bbf0b18045f957e18094b4421))
* **api:** document MCP connector deprecation ([#202](https://github.com/openai/openai-cli/issues/202)) ([1d4e76c](https://github.com/openai/openai-cli/commit/1d4e76cb3cbed9992fc6a8b73e50249d2033e575))
* **api:** update image request examples ([#197](https://github.com/openai/openai-cli/issues/197)) ([4b75e9b](https://github.com/openai/openai-cli/commit/4b75e9b635fb50e393de25e1eece3670ba6dd575))
* **deps:** bump the codeql group across 1 directory with 2 updates ([#161](https://github.com/openai/openai-cli/issues/161)) ([97734f3](https://github.com/openai/openai-cli/commit/97734f3cde7561fb2d80fe0d4d999bbf154aec96))
* **deps:** bump the codeql group across 1 directory with 2 updates ([#224](https://github.com/openai/openai-cli/issues/224)) ([0713595](https://github.com/openai/openai-cli/commit/07135953b2464dbae26b8e9548e24dacc070e6ba))
* **deps:** bump the go-minor-and-patch group across 1 directory with 2 updates ([#144](https://github.com/openai/openai-cli/issues/144)) ([aa1158d](https://github.com/openai/openai-cli/commit/aa1158d58e6d7843ec96f82501ac0cd923b8d60b))
* **deps:** update openai-go to v3.61.0 ([#184](https://github.com/openai/openai-cli/issues/184)) ([de52b2e](https://github.com/openai/openai-cli/commit/de52b2eddf77d9b755c7ae7c3d8888d5e818d272))
* **deps:** update openai-go to v3.63.0 ([#198](https://github.com/openai/openai-cli/issues/198)) ([3f2c883](https://github.com/openai/openai-cli/commit/3f2c88329016167f61785328daf118cb7708a67a))
* **deps:** update openai-go to v3.63.1 ([#204](https://github.com/openai/openai-cli/issues/204)) ([eb415e5](https://github.com/openai/openai-cli/commit/eb415e53c0dff9f31a43da13e21056722ed85b97))
* **deps:** update openai-go to v3.64.0 ([#206](https://github.com/openai/openai-cli/issues/206)) ([0169bff](https://github.com/openai/openai-cli/commit/0169bff050ff9c05c4609b669c06709306d464fa))
* **deps:** update openai-go to v3.64.2 ([#225](https://github.com/openai/openai-cli/issues/225)) ([a7859e5](https://github.com/openai/openai-cli/commit/a7859e5da08e34482fa800da1fb7d17ad6806550))


### Documentation

* correct bootstrap dependency check description ([#181](https://github.com/openai/openai-cli/issues/181)) ([4aa657c](https://github.com/openai/openai-cli/commit/4aa657cbc6a0db66ea3954e26bdb1133e4c5b9f6))

## [1.15.0](https://github.com/openai/openai-cli/compare/v1.14.0...v1.15.0) (2026-09-10)


### Features

* **api:** Add Live API ([#182](https://github.com/openai/openai-cli/issues/182)) ([764e7f5](https://github.com/openai/openai-cli/commit/764e7f56a8d91bc58a1a158b46fc47950545c5c4))


### Chores

* **deps:** update openai-go to v3.60.0 ([#173](https://github.com/openai/openai-cli/issues/173)) ([b8c81ec](https://github.com/openai/openai-cli/commit/b8c81ecdcb79aa56324b569de960505b68b86f97))

## [1.14.0](https://github.com/openai/openai-cli/compare/v1.13.0...v1.14.0) (2026-09-09)


### Features

* **api:** Add service account key expiration flag ([#177](https://github.com/openai/openai-cli/issues/177)) ([2e1a179](https://github.com/openai/openai-cli/commit/2e1a179921d0e1b354443f123e37a74002c9ae69))

## [1.13.0](https://github.com/openai/openai-cli/compare/v1.12.0...v1.13.0) (2026-09-08)


### Features

* **api:** document GPT Image 2.5 models and image options ([#175](https://github.com/openai/openai-cli/issues/175)) ([7a9994d](https://github.com/openai/openai-cli/commit/7a9994dd5c6cfe232fc2ebc548a3da95dddb7439))


### Bug Fixes

* **autocomplete:** preserve literal completion-token arguments ([#171](https://github.com/openai/openai-cli/issues/171)) ([01123b7](https://github.com/openai/openai-cli/commit/01123b72b815eaea30cdf0e50608c88b485a9eab))
* **output:** honor case-insensitive explore format ([#169](https://github.com/openai/openai-cli/issues/169)) ([774c240](https://github.com/openai/openai-cli/commit/774c2406188d66c03a1521b31be2c24d9bda4e46))

## [1.12.0](https://github.com/openai/openai-cli/compare/v1.11.0...v1.12.0) (2026-09-08)


### Features

* **api:** Add prompt cache comparison response option ([#155](https://github.com/openai/openai-cli/issues/155)) ([aa76074](https://github.com/openai/openai-cli/commit/aa76074316940047ec21439f541ce5eb06f6b03d))
* **api:** add service-account API key expiration fields ([#157](https://github.com/openai/openai-cli/issues/157)) ([bf513cb](https://github.com/openai/openai-cli/commit/bf513cb5a3b4cedd0ddb1444ae5c4ae9e937c4cd))


### Bug Fixes

* **autocomplete:** use distinct status for no completion ([#164](https://github.com/openai/openai-cli/issues/164)) ([094a4ab](https://github.com/openai/openai-cli/commit/094a4ab96e80c2952c830ae36a9a9f009617e5ec))
* **jsonview:** preserve raw API fields in lazy-loaded rows ([#163](https://github.com/openai/openai-cli/issues/163)) ([0bf80c0](https://github.com/openai/openai-cli/commit/0bf80c0689dc5bfab37b59b794551a176fb51643))
* **jsonview:** retain lazy-loaded items across JSON toggles ([#160](https://github.com/openai/openai-cli/issues/160)) ([ef1b383](https://github.com/openai/openai-cli/commit/ef1b3834c0b4c3f2894763b7d64fe77f73a3e405))
* **output:** preserve upstream errors when streaming ([#159](https://github.com/openai/openai-cli/issues/159)) ([1048593](https://github.com/openai/openai-cli/commit/1048593bafed0651612f770a831cd69ffb5a2c22))
* **requestflag:** handle inner fields after null array objects ([#162](https://github.com/openai/openai-cli/issues/162)) ([659eb06](https://github.com/openai/openai-cli/commit/659eb06c723e47c5b77ada0a5e0359ddfc0615a1))
* **requestflag:** preserve piped query arrays ([#167](https://github.com/openai/openai-cli/issues/167)) ([265cf63](https://github.com/openai/openai-cli/commit/265cf6378ab2e6a27c17bc7c0b6a6cbe6dbd477b))


### Chores

* **api:** correct function argument completion event fields (openapi-545) ([#156](https://github.com/openai/openai-cli/issues/156)) ([465360b](https://github.com/openai/openai-cli/commit/465360b4ebbdb67f628cb27a205bfe521ce6cc79))
* **api:** document incomplete web search call status ([#153](https://github.com/openai/openai-cli/issues/153)) ([a843cea](https://github.com/openai/openai-cli/commit/a843cea03c7b7c16cf8b0e1be29b8af7cb7131db))
* **api:** document throttling and model overload responses — SDK-235 ([#158](https://github.com/openai/openai-cli/issues/158)) ([76d3959](https://github.com/openai/openai-cli/commit/76d39597222479badd615675f485e7848b0dc697))
* migrate to forked steady ([#152](https://github.com/openai/openai-cli/issues/152)) ([9c18bfa](https://github.com/openai/openai-cli/commit/9c18bfa7be50ee60a4335770c669c5b970c52325))

## [1.11.0](https://github.com/openai/openai-cli/compare/v1.10.0...v1.11.0) (2026-09-03)


### Features

* **api:** add gpt-6-astra and related features ([#150](https://github.com/openai/openai-cli/issues/150)) ([9d6a64f](https://github.com/openai/openai-cli/commit/9d6a64f97fb24522842d1894cb96b44298e00431))


### Chores

* **deps:** update openai-go to v3.55.0 ([#148](https://github.com/openai/openai-cli/issues/148)) ([63d2931](https://github.com/openai/openai-cli/commit/63d29317bb5b660ff15b94c06138cdaaa76222d4))
* **deps:** update openai-go to v3.56.0 ([#151](https://github.com/openai/openai-cli/issues/151)) ([d5ddcd4](https://github.com/openai/openai-cli/commit/d5ddcd46aae69e2c8af5e8f5910a99f6c2a86137))

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
