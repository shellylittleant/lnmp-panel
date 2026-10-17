import { createApp } from "vue";
import App from "./App.vue";
import { setCsrf, setUnauthorized } from "./api";
import { router } from "./router";

setUnauthorized(() => {
  setCsrf("");
  if (router.currentRoute.value.path !== "/login") {
    void router.replace("/login");
  }
});

createApp(App).use(router).mount("#app");
