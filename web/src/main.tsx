import { render } from "preact";
import App from "./App";
import "./index.css";
import "./themes.css";
import { initTheme } from "./theme";

initTheme();

render(<App />, document.getElementById("root")!);
