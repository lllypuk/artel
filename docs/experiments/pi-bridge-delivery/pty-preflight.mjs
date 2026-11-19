// Resolve the public `pi` CLI after validating the actual Node and package versions.
import { pinnedPi } from './pi-package.mjs';
console.log(pinnedPi(process.env.PI_TEST_PACKAGE).cli);
