import { ensureAuthentication } from "./auth.js";
import "./install-prompt.js";

if (await ensureAuthentication()) await import("./app.js");
