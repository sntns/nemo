# Changelog

## [0.14.0](https://github.com/sntns/nemo/compare/v0.13.0...v0.14.0) (2026-11-10)


### Features

* an installation provided outside the stack, which gives its outputs ([ef75ca8](https://github.com/sntns/nemo/commit/ef75ca883c185124e521c2ab59aa6ff5bbd4531d))

## [0.13.0](https://github.com/sntns/nemo/compare/v0.12.5...v0.13.0) (2026-11-10)


### Features

* a bundle gives outputs, and reads a parameter from a dependency's ([4bae86a](https://github.com/sntns/nemo/commit/4bae86ad831c489d2f9f9f4a6b751a86948b72e1))

## [0.12.5](https://github.com/sntns/nemo/compare/v0.12.4...v0.12.5) (2026-11-09)


### Bug Fixes

* a new version alone restarts no service ([57e33f5](https://github.com/sntns/nemo/commit/57e33f5c197755cc5305638ee75a862d59388102))

## [0.12.4](https://github.com/sntns/nemo/compare/v0.12.3...v0.12.4) (2026-11-06)


### Bug Fixes

* give a Swarm job no update policy of gill's ([a27f154](https://github.com/sntns/nemo/commit/a27f15473e2dc000c67bebf1f3ba5811da5fb535))
* let a Swarm update continue past a failed task, unless the bundle says otherwise ([195196b](https://github.com/sntns/nemo/commit/195196bf4ab1f37bdf761d606c553a962921801c))

## [0.12.3](https://github.com/sntns/nemo/compare/v0.12.2...v0.12.3) (2026-11-06)


### Bug Fixes

* build with Go 1.26.10 and klauspost/compress 1.20.2 ([97134aa](https://github.com/sntns/nemo/commit/97134aad0d35883cf368c90acb977905779b9b32))
* resume a Swarm update that a failed task paused ([5866403](https://github.com/sntns/nemo/commit/5866403f959534d8107e4cbfcb55455163a0ef22))

## [0.12.2](https://github.com/sntns/nemo/compare/v0.12.1...v0.12.2) (2026-11-05)


### Bug Fixes

* refuse a .. in the records' archive before anything is made of it ([589c163](https://github.com/sntns/nemo/commit/589c1637e9a8fc9e6a057b3de7378931600244e3))

## [0.12.1](https://github.com/sntns/nemo/compare/v0.12.0...v0.12.1) (2026-11-05)


### Bug Fixes

* recreate a Swarm service whose mode changes ([5af7d2b](https://github.com/sntns/nemo/commit/5af7d2be6ef4590c50eb18c2c53fd25bba1cd103))
* refuse a published port out of range, and any .. in the records ([f6eca1b](https://github.com/sntns/nemo/commit/f6eca1b0ee7f6725cb264c7b7223bc2814cafa88))

## [0.12.0](https://github.com/sntns/nemo/compare/v0.11.0...v0.12.0) (2026-11-05)


### Features

* a compact live view, with --all for every service ([c0a6d06](https://github.com/sntns/nemo/commit/c0a6d0667ce4708733db8125cc05552fa6834304))
* give each object installations share an installation of its own ([8e1dbf0](https://github.com/sntns/nemo/commit/8e1dbf0950a68211cacf2f65dea009b3fa8f382d))
* load gill alone for the actions that run no service, with no cap ([70a5183](https://github.com/sntns/nemo/commit/70a5183649bfeab5748dfebdadcc22609e907bcc))
* plan a stack before acting, and run its installations in parallel ([1f80396](https://github.com/sntns/nemo/commit/1f80396fbf328c059a55f6f2c717267c5e119235))


### Bug Fixes

* a square of the meter for each service, the color of its line ([c46a4e6](https://github.com/sntns/nemo/commit/c46a4e6857b611d19e274d996d9de738f3397b76))
* color the meters of the compact view ([314ff8f](https://github.com/sntns/nemo/commit/314ff8fa51c394d480a497086b942c7f8a79b920))
* declare a shared config or secret without labels when it has none ([8a76b9c](https://github.com/sntns/nemo/commit/8a76b9c674bb8742fbd5f3f90411bb154c9ba405))
* leave a shared object to its own installation ([ac30f2d](https://github.com/sntns/nemo/commit/ac30f2db1479c1ab7769fc87c25cbc92f78fdc37))
* show and start a down in the order its installations go ([934210b](https://github.com/sntns/nemo/commit/934210b57345310bdf8749c6701080d91aaf68ac))

## [0.11.0](https://github.com/sntns/nemo/compare/v0.10.0...v0.11.0) (2026-11-02)


### ⚠ BREAKING CHANGES

* drop x-nemo-credential and the parameters of x-nemo-bundle

### Features

* drop x-nemo-credential and the parameters of x-nemo-bundle ([cae2e69](https://github.com/sntns/nemo/commit/cae2e69c7398dc8ec1a42d390d3dc893b76ff2ce))
* let a stack override a bundle's compose, per installation ([3ea92a7](https://github.com/sntns/nemo/commit/3ea92a73d30570b55e3d72467894ecb075c75741))

## [0.10.0](https://github.com/sntns/nemo/compare/v0.9.0...v0.10.0) (2026-11-02)


### Features

* **release:** publish deb, rpm and apk packages, SBOMs, signed checksums and provenance, and sign gill ([f8cfc23](https://github.com/sntns/nemo/commit/f8cfc23caca5f346595906b19c7f637d2bd45e1e))


### Bug Fixes

* give a stack's installations only the parameters the stack gives ([00b2bcd](https://github.com/sntns/nemo/commit/00b2bcdaa6dc1b134dc73ab72f3ac8021ace386a))

## [0.9.0](https://github.com/sntns/nemo/compare/v0.8.0...v0.9.0) (2026-10-30)


### Features

* declare a bundle's parameters in x-nemo-bundle ([409e7c9](https://github.com/sntns/nemo/commit/409e7c9a599e5f263e7d74f1be454ef7303767c3))

## [0.8.0](https://github.com/sntns/nemo/compare/v0.7.0...v0.8.0) (2026-10-30)


### Features

* require a secret's credential on the bundle's parameters, with x-nemo-credential ([c99ad42](https://github.com/sntns/nemo/commit/c99ad42e9256fee173f0d70cff3e4b667e6d1271))


### Bug Fixes

* build a config whose content needs a parameter without a default ([cef6a29](https://github.com/sntns/nemo/commit/cef6a297ffb5c759ecb9b02512df8af2834cc4c1))

## [0.7.0](https://github.com/sntns/nemo/compare/v0.6.0...v0.7.0) (2026-10-29)


### Features

* honour io.cnab.parameter-sources ([d4e7a3f](https://github.com/sntns/nemo/commit/d4e7a3f4e23f0857db6bdb1814b35205a07414c1))


### Bug Fixes

* **gill:** take an empty parameter of a type with no empty value as not given ([d50e5fa](https://github.com/sntns/nemo/commit/d50e5faf32053828ea335a0ce535c5ba13a2e3cd))

## [0.6.0](https://github.com/sntns/nemo/compare/v0.5.1...v0.6.0) (2026-10-29)


### Features

* keep a stack's records on the storage of the target's records type, held by the whole swarm ([44d97a1](https://github.com/sntns/nemo/commit/44d97a1ee8a0be9520c4777e5e03096a70872014))
* record a value source without its value ([ad79215](https://github.com/sntns/nemo/commit/ad792158bca78b46e9d9ffa376206a4585fb5e9a))

## [0.5.1](https://github.com/sntns/nemo/compare/v0.5.0...v0.5.1) (2026-10-29)


### Bug Fixes

* record an action whose context ends while it runs ([f1222e5](https://github.com/sntns/nemo/commit/f1222e537c337432629b827ca8a161f2a0a84ab7))

## [0.5.0](https://github.com/sntns/nemo/compare/v0.4.0...v0.5.0) (2026-10-29)


### Features

* give a tcp:// target its TLS in its manifest, each part from a source ([a92c226](https://github.com/sntns/nemo/commit/a92c2267a6e4a4cc0937d6df4ae9acd197bb1b91))
* keep a stack's records on its target, for every home that imports it ([16c7ea5](https://github.com/sntns/nemo/commit/16c7ea56810f065cab91af28013d0161f506367c))


### Bug Fixes

* **runtime:** connect to several Dockers at once without a race on the CLI's configuration ([6db90e6](https://github.com/sntns/nemo/commit/6db90e68e5c783997627b27f92985840a841872e))

## [0.4.0](https://github.com/sntns/nemo/compare/v0.3.2...v0.4.0) (2026-10-28)


### Features

* **cli:** choose the target's Docker in the editor ([90c1e4f](https://github.com/sntns/nemo/commit/90c1e4fe36d2b34dbe191dd5353b8a47c39fd688))
* **cli:** make the volume types in the editor, from the targets guide's ([359aacb](https://github.com/sntns/nemo/commit/359aacb2e9d09f46966e54529605efed7d205e4b))
* **cli:** show an installation's own values in the editor's review ([7b52d9c](https://github.com/sntns/nemo/commit/7b52d9cd0d37044b28338a33b104451b0724acc7))


### Bug Fixes

* **cli:** keep the live progress of up and down within the terminal's height ([b7dbf88](https://github.com/sntns/nemo/commit/b7dbf88229b2778c317dbb35b5e14dd6cf4d6053))
* **cli:** scroll the editor's review, and lists, to the terminal's height ([00eddf8](https://github.com/sntns/nemo/commit/00eddf83693e731b799e63628e11a77d321fbec3))

## [0.3.2](https://github.com/sntns/nemo/compare/v0.3.1...v0.3.2) (2026-10-28)


### Bug Fixes

* **bundle:** build a bundle whose typed fields hold variables ([ef251b8](https://github.com/sntns/nemo/commit/ef251b845c17d9034764889c52fa039c3ca6befb))
* **cnab:** load the numbers of a bundle's compose project as a compose file does ([68ead23](https://github.com/sntns/nemo/commit/68ead23f1d08745d6933c3fcc97c685a122a1ed1))
* **compose:** wait until a started container stays up ([ddc8a50](https://github.com/sntns/nemo/commit/ddc8a5058cdf9f6f62b2f4dfc04790c83e4d081e))
* **installer:** give the daemon the cpus the compose file wrote ([c7261e1](https://github.com/sntns/nemo/commit/c7261e1c6e842d25832f5b4185e44f37ed666fc4))
* **installer:** report a link, or a shared namespace, for what it is ([3a605ce](https://github.com/sntns/nemo/commit/3a605ce8dfbd0b9a282dbe4280a37667e7e5f642))
* **stack:** take down a stack that has nothing installed, without an error ([665c47b](https://github.com/sntns/nemo/commit/665c47ba4f2ed2c5e7af6aa5d579fd662d068e17))
* **swarm:** see a rollback when the installer's clock is ahead of the manager's ([7204a25](https://github.com/sntns/nemo/commit/7204a25bd9e723c6342d13f48e3cbbce43e886ba))

## [0.3.1](https://github.com/sntns/nemo/compare/v0.3.0...v0.3.1) (2026-10-27)


### Bug Fixes

* **swarm:** wait for a restarted task that Swarm has not scheduled yet ([ef84cd3](https://github.com/sntns/nemo/commit/ef84cd3f3f067970c057c571ab68fe0352c28412))

## [0.3.0](https://github.com/sntns/nemo/compare/v0.2.1...v0.3.0) (2026-10-27)


### Features

* check every bundle before up installs the first ([4ccb456](https://github.com/sntns/nemo/commit/4ccb45603658a111228c4c0e7547221935f47e82))
* **cli:** edit stacks in an editor, and nemo stack edit ([d8742f6](https://github.com/sntns/nemo/commit/d8742f62c8edfd495591cd3fe31263c2d9c26c0e))
* **swarm:** show why tasks restart, and stop once Swarm gives up ([4440f37](https://github.com/sntns/nemo/commit/4440f37409e19356324d9059d0f34bb3dfe2ff0f))


### Bug Fixes

* **swarm:** let Swarm restart the tasks of a paused update ([c51bbdf](https://github.com/sntns/nemo/commit/c51bbdf1290562c3fe2b3ce24c47e95bbf386a41))
* **swarm:** show only the failed service as failed ([5a1f2eb](https://github.com/sntns/nemo/commit/5a1f2eb6d01915d5c5553f7c09017146de6eee63))

## [0.2.1](https://github.com/sntns/nemo/compare/v0.2.0...v0.2.1) (2026-10-27)


### Bug Fixes

* **cli:** create the swarm on a machine with several network interfaces ([c630685](https://github.com/sntns/nemo/commit/c630685f87b22c0fa57613e1a175a16a8bb4df1e))

## [0.2.0](https://github.com/sntns/nemo/compare/v0.1.1...v0.2.0) (2026-10-26)


### Features

* **cli:** complete the names of the stacks ([69e9923](https://github.com/sntns/nemo/commit/69e9923ab24b1b8bd0b0fa595cf96f9ca792043a))
* **compose:** count each service's containers as they start ([88198dd](https://github.com/sntns/nemo/commit/88198ddb3060ae00b162affefad463630b5d622a))


### Bug Fixes

* **nemo:** take down the installations whose install failed ([ba74066](https://github.com/sntns/nemo/commit/ba7406619caad4e8f13318aec5af730f3a3f3f3a))

## [0.1.1](https://github.com/sntns/nemo/compare/v0.1.0...v0.1.1) (2026-10-26)


### Build System

* let a release that failed half way run again ([b15abdf](https://github.com/sntns/nemo/commit/b15abdf419b6ebdd274eb800a92340c046e0eaf5))

## 0.1.0 (2026-10-26)


### Features

* **bundle:** build a bundle from compose files, and move it ([fb3ac27](https://github.com/sntns/nemo/commit/fb3ac271b474b983aaf263d121d8c00897cd0bfb))
* **cli:** create a stack in a form ([ec9b143](https://github.com/sntns/nemo/commit/ec9b143d1ac73167fe15ef5698a1bd6231181765))
* **cli:** the nemo command ([18063e7](https://github.com/sntns/nemo/commit/18063e72368c854196d4eedfc4fe0d4466d753ef))
* **cnab:** read bundles, with nemo's extensions and their schemas ([1f8f3ea](https://github.com/sntns/nemo/commit/1f8f3ea08c7f5c0a2670cddcec6f74e744438d92))
* **credentials:** resolve credentials from their sources ([450e18f](https://github.com/sntns/nemo/commit/450e18fa9a898f27d7642e2d0ee8f2dff7b7b82e))
* **docker:** a client of the Docker Engine for both orchestrators ([b4a73b1](https://github.com/sntns/nemo/commit/b4a73b1314746735730ee17a22d9e97595ce4c16))
* **gill:** the invocation image ([7c54bdd](https://github.com/sntns/nemo/commit/7c54bdd7531a9d0664a69d8a4ab01da4a3a21793))
* **installer:** deploy with Compose ([e05063b](https://github.com/sntns/nemo/commit/e05063b59cb02a3c50348fe5c97cdbaa83dae6fd))
* **installer:** deploy with Swarm ([5f4dfd8](https://github.com/sntns/nemo/commit/5f4dfd85f5e6e4350dfeec4699f29c077d375d01))
* **installer:** read the CNAB operation and load the compose project ([ec53f78](https://github.com/sntns/nemo/commit/ec53f786702a11953408f54c67a5315fa2b813bc))
* **nemo:** bring a stack to its manifest ([21cc9bb](https://github.com/sntns/nemo/commit/21cc9bb1fea97af42fff53b70d4b47d9f11611d8))
* **nemo:** install, upgrade and uninstall a bundle ([186b49f](https://github.com/sntns/nemo/commit/186b49fbe9522fd7b0d76243aaff37cbf835bf71))
* **nemo:** read where a stack stands, and its logs ([210acaf](https://github.com/sntns/nemo/commit/210acaff32443b98055bd1050dae0f915d83c647))
* **runtime:** run a bundle's actions, as a CNAB runtime ([5e43055](https://github.com/sntns/nemo/commit/5e4305562fba6c831fabcbc09a253b997f9d252f))
* **stack:** describe a stack in a manifest ([6fafd3c](https://github.com/sntns/nemo/commit/6fafd3c7e52d4882b3ec440030bbf5ad9b16a444))
* **storage:** keep installations, runs, results and outputs ([4ad8a30](https://github.com/sntns/nemo/commit/4ad8a30188e2eff094ae106d3727c2cfc7e7fb9a))
* targets, labels and progress events ([23cb1bd](https://github.com/sntns/nemo/commit/23cb1bd94c64e32fc0252b708edfc368dc76c88d))
* **transfer:** load a bundle's images into the target's daemon ([524bd2b](https://github.com/sntns/nemo/commit/524bd2b367d58ad832b2064aad67e7cef149a942))
