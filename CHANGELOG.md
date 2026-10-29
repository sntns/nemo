# Changelog

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
