# Changelog

## [0.4.0](https://github.com/aaronflorey/mdc/compare/v0.3.1...v0.4.0) (2026-07-19)


### Features

* **cli:** add compose build routing with bounded buffering ([d761e8e](https://github.com/aaronflorey/mdc/commit/d761e8e7d51e381617d1f96981c4e66966d860ae))
* **cli:** add live up status board and tty smoke test ([90ce2ba](https://github.com/aaronflorey/mdc/commit/90ce2ba409ecab5e378c8cb3845edbc30d25e1a4))
* **cli:** add multi-project compose runner ([ff45f99](https://github.com/aaronflorey/mdc/commit/ff45f99d86036f89cfe676252b1e8d07bca4f741))
* **cli:** add top-level images aggregation with fallback handling ([b39f116](https://github.com/aaronflorey/mdc/commit/b39f1165bc2c312be7adbb576021d305b524f497))
* **cli:** enforce serial-only events mode ([d02dd88](https://github.com/aaronflorey/mdc/commit/d02dd88e7e4bc4bf2257b711034e8eb2a5d1d206))
* **cli:** forward stdin to docker compose ([a9164f2](https://github.com/aaronflorey/mdc/commit/a9164f229b230478dd07bca49e27b21341578cdd))
* **cli:** preserve attached up streaming ([5231bfa](https://github.com/aaronflorey/mdc/commit/5231bfae42a4dbbb819cf7c65cf9f0c90236121a))
* **cli:** style ps output with colors when writing to a tty ([be8c01c](https://github.com/aaronflorey/mdc/commit/be8c01c329e85458c989fe0bb403ebca86d2eba1))
* **cli:** tighten logs follow-mode routing and failure coverage ([1f3d665](https://github.com/aaronflorey/mdc/commit/1f3d6657ada60bbb5e77e063f8395ebedb75f921))
* improve release workflow ([9e2549a](https://github.com/aaronflorey/mdc/commit/9e2549a58d607be050aac4cbc86cac4ee7dcaf4c))


### Bug Fixes

* **cli:** cancel compose runs on interrupt ([cc6e585](https://github.com/aaronflorey/mdc/commit/cc6e585c0244aa6e1509fd7fde6e08d5a2425f3d))
* **cli:** detect only top-level compose ps ([4cd1a6c](https://github.com/aaronflorey/mdc/commit/4cd1a6ce59701c1b40f14484a1cc416e3d40ce6b))
* **cli:** honor quiet targets in buffered output ([7204ebb](https://github.com/aaronflorey/mdc/commit/7204ebb9165af912e9e7d6c05df9ed437ecb74ac))
* **cli:** isolate compose project names ([150cd3e](https://github.com/aaronflorey/mdc/commit/150cd3e6c7a5f7cf76f9fd2f287dcd5f5453647d))
* **cli:** preserve compose directory names ([afbc04f](https://github.com/aaronflorey/mdc/commit/afbc04f1e8cd3cbb1ea6c771df0021cee40156eb))
* **cli:** reduce pull output noise ([a4447c9](https://github.com/aaronflorey/mdc/commit/a4447c960f883a6ae9ea34be7c10eee61e0d0fbf))
* **cli:** stream compose output per target ([55abab8](https://github.com/aaronflorey/mdc/commit/55abab8219ab5589cd996625bef897c798bb57d2))
* **release:** match homebrew archive ids ([c037826](https://github.com/aaronflorey/mdc/commit/c0378268620b5ad6336dc3ba26cce70245956386))

## [0.3.1](https://github.com/aaronflorey/mdc/compare/v0.3.0...v0.3.1) (2026-05-19)


### Bug Fixes

* **cli:** reduce pull output noise ([c41a44d](https://github.com/aaronflorey/mdc/commit/c41a44d6bf58b1922ae0cc7bb6de9fa159a20ccc))

## [0.3.0](https://github.com/aaronflorey/mdc/compare/v0.2.1...v0.3.0) (2026-05-19)


### Features

* **cli:** Add multi-project compose runner ([8d6bfc4](https://github.com/aaronflorey/mdc/commit/8d6bfc414479b4ab068b19a90f741e785d2b4f77))
* improve release workflow ([9f44890](https://github.com/aaronflorey/mdc/commit/9f448903705894534ec0e836722f7e68a7b37ab2))


### Bug Fixes

* **cli:** Cancel compose runs on interrupt ([f0d274f](https://github.com/aaronflorey/mdc/commit/f0d274f469c70a7a8b85ef171f3a7281432f160c))
* **cli:** Detect only top-level compose ps ([5462650](https://github.com/aaronflorey/mdc/commit/5462650a3ee493a01d0a5fd541ccac98b690146d))
* **cli:** Isolate compose project names ([e74f461](https://github.com/aaronflorey/mdc/commit/e74f461af5a2f382f5111ce4084a7446b9ab49ff))
* **cli:** preserve compose directory names ([504fbd8](https://github.com/aaronflorey/mdc/commit/504fbd8879d3bf810655367582e9415d33deb61c))
* **cli:** Stream compose output per target ([1981a55](https://github.com/aaronflorey/mdc/commit/1981a5598e01056e2fbb54ede0f00348a1c138e6))
* **release:** Match Homebrew archive ids ([1375f99](https://github.com/aaronflorey/mdc/commit/1375f9922a554d14d949f9cceaca72a2c865aac7))

## [0.2.0](https://github.com/aaronflorey/mdc/compare/v0.1.0...v0.2.0) (2026-04-15)


### Features

* **cli:** Add multi-project compose runner ([8d6bfc4](https://github.com/aaronflorey/mdc/commit/8d6bfc414479b4ab068b19a90f741e785d2b4f77))


### Bug Fixes

* **cli:** Cancel compose runs on interrupt ([f0d274f](https://github.com/aaronflorey/mdc/commit/f0d274f469c70a7a8b85ef171f3a7281432f160c))
* **cli:** Detect only top-level compose ps ([5462650](https://github.com/aaronflorey/mdc/commit/5462650a3ee493a01d0a5fd541ccac98b690146d))
* **cli:** Isolate compose project names ([e74f461](https://github.com/aaronflorey/mdc/commit/e74f461af5a2f382f5111ce4084a7446b9ab49ff))
