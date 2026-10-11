import { ensureAuthentication } from "./auth.js";
import "./install-prompt.js";

if (await ensureAuthentication()) {
  if (location.pathname === "/alexa") await (await import("./alexa.js")).startAlexaLink();
  else await import("./app.js");
}
