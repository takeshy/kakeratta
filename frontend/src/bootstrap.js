import { ensureAuthentication } from "./auth.js";

if (await ensureAuthentication()) await import("./app.js");
