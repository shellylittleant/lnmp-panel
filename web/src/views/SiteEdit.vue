<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { NAlert, NButton, NCard, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, useMessage } from "naive-ui";
import { api } from "../api";

const route = useRoute();
const router = useRouter();
const msg = useMessage();
const id = route.params.id as string;
const drifted = ref(false);
const rawMode = ref(false);
const force = ref(false);
const name = ref("");
const domains = ref("");
const root = ref("");
const php = ref("");
const proxy = ref("");
const conf = ref("");
const http2 = ref(true);
const tpls = ref<{ label: string; value: number }[]>([]);
const nginxTpl = ref<number | null>(null);
const rewriteTpl = ref<number | null>(null);
const phpOpts = ref<{ label: string; value: string }[]>([{ label: "静态（无 PHP）", value: "" }]);
type Redirect = { from_path: string; to_url: string; code: number };
const redirects = ref<Redirect[]>([]);

onMounted(async () => {
  const pv = await api<{ items: { version: string; installed: boolean }[] }>("GET", "/php/versions");
  if (pv.ok) {
    phpOpts.value = [{ label: "静态（无 PHP）", value: "" }].concat(
      (pv.data.items || []).filter((x) => x.installed).map((x) => ({ label: x.version, value: x.version })),
    );
  }
  const t = await api<{ items: { id: number; type: string; name: string; origin: string }[] }>("GET", "/templates");
  if (t.ok) {
    tpls.value = (t.data.items || [])
      .filter((x) => x.type === "nginx_site" || x.type === "nginx_rewrite")
      .map((x) => ({ label: `${x.origin === "official" ? "官方" : "用户"} / ${x.type} / ${x.name}`, value: x.id }));
  }
  const r = await api<any>("GET", "/sites/" + id);
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  const d = r.data;
  name.value = d.name;
  domains.value = (d.domains || []).join(", ");
  root.value = d.root;
  php.value = d.php_version || "";
  proxy.value = d.proxy_pass || "";
  drifted.value = d.drifted;
  rawMode.value = d.raw_mode;
  http2.value = d.http2;
  nginxTpl.value = d.nginx_template_id;
  rewriteTpl.value = d.rewrite_template_id;
  const n = await api<{ content: string }>("GET", "/sites/" + id + "/nginx");
  if (n.ok) conf.value = n.data.content;
  const rd = await api<{ items: Redirect[] }>("GET", "/sites/" + id + "/redirects");
  if (rd.ok) redirects.value = rd.data.items || [];
});

async function save() {
  const r = await api("PUT", "/sites/" + id, {
    name: name.value,
    domains: domains.value.split(",").map((x) => x.trim()).filter(Boolean),
    root: root.value,
    php_version: php.value,
    proxy_pass: proxy.value,
    http2: http2.value,
    nginx_template_id: nginxTpl.value ?? "",
    rewrite_template_id: rewriteTpl.value ?? "",
    force: force.value,
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已保存，配置在 1 到 2 秒内生效");
  drifted.value = false;
}

async function saveRaw() {
  const r = await api("PUT", "/sites/" + id + "/nginx", { content: conf.value, force: force.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("原文已保存，配置在 1 到 2 秒内生效");
  rawMode.value = true;
}

async function useTpl() {
  const r = await api("POST", "/sites/" + id + "/nginx/use-template", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已恢复模板渲染");
  rawMode.value = false;
}

async function issue() {
  if (!window.confirm("将删除当前证书并重新签发，成功后立刻重载 Nginx。原文模式下只改证书路径，不重写整份配置。")) return;
  const r = await api("POST", "/sites/" + id + "/ssl/issue", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("证书已签发并写回站点，配置在 1 到 2 秒内生效");
}

const certPem = ref("");
const keyPem = ref("");

async function importManual() {
  if (!window.confirm("将覆盖该站点现有证书和私钥，并立刻改 Nginx 配置。")) return;
  const r = await api<{ warning?: string }>("POST", "/sites/" + id + "/ssl/import", { cert_pem: certPem.value, key_pem: keyPem.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  if (r.data.warning) msg.warning(r.data.warning);
  else msg.success("手动证书已保存");
}

async function del() {
  if (!window.confirm("删除站点 " + name.value + "：会删除 Nginx 配置和证书，网站目录默认保留。")) return;
  const r = await api("DELETE", "/sites/" + id, undefined);
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  router.push("/sites");
}

function addRedirect() {
  redirects.value.push({ from_path: "/", to_url: "https://", code: 301 });
}

async function saveRedirects() {
  const r = await api("PUT", "/sites/" + id + "/redirects", { items: redirects.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("跳转已保存");
}
</script>

<template>
  <n-space vertical>
    <n-alert v-if="drifted" type="warning" title="配置漂移">
      磁盘上的配置与面板上次写入不一致，已显示磁盘内容。保存将以你现在提交的内容覆盖磁盘。
    </n-alert>
    <n-alert v-if="rawMode" type="info" title="原文模式">当前为原文模式，表单只读。请先点「恢复为模板渲染」再改表单。</n-alert>
    <n-card title="站点">
      <n-form>
        <n-form-item label="名称"><n-input v-model:value="name" :disabled="rawMode" /></n-form-item>
        <n-form-item label="域名"><n-input v-model:value="domains" :disabled="rawMode" /></n-form-item>
        <n-form-item label="根目录">
          <n-space>
            <n-input v-model:value="root" :disabled="rawMode" />
            <n-button @click="router.push({ path: '/files', query: { path: root } })">打开目录</n-button>
          </n-space>
        </n-form-item>
        <n-form-item label="PHP 版本"><n-select v-model:value="php" :options="phpOpts" :disabled="rawMode" /></n-form-item>
        <n-form-item label="反代目标"><n-input v-model:value="proxy" :disabled="rawMode" /></n-form-item>
        <n-form-item label="HTTP/2"><n-checkbox v-model:checked="http2" :disabled="rawMode" /></n-form-item>
        <n-form-item label="站点模板">
          <n-select v-model:value="nginxTpl" :options="tpls.filter((x) => x.label.includes('nginx_site'))" clearable :disabled="rawMode" />
        </n-form-item>
        <n-form-item label="伪静态">
          <n-select v-model:value="rewriteTpl" :options="tpls.filter((x) => x.label.includes('nginx_rewrite'))" clearable :disabled="rawMode" />
        </n-form-item>
        <n-checkbox v-model:checked="force">确认覆盖磁盘（漂移时必勾）</n-checkbox>
        <n-space style="margin-top: 12px">
          <n-button type="primary" :disabled="rawMode" @click="save">保存表单</n-button>
          <n-button @click="issue">申请/签发证书</n-button>
          <n-button @click="useTpl">恢复为模板渲染</n-button>
          <n-button type="error" @click="del">删除站点</n-button>
        </n-space>
      </n-form>
    </n-card>
    <n-card title="手动填入证书">
      <p>粘贴证书链 PEM 与私钥 PEM。过期拒绝；域名不覆盖只警告。手动证书不自动续期。</p>
      <n-form-item label="证书链"><n-input v-model:value="certPem" type="textarea" :autosize="{ minRows: 6 }" /></n-form-item>
      <n-form-item label="私钥"><n-input v-model:value="keyPem" type="textarea" :autosize="{ minRows: 6 }" /></n-form-item>
      <n-button @click="importManual">保存手动证书</n-button>
    </n-card>
    <n-card title="301 / 跳转">
      <p v-if="rawMode">原文模式下不能改跳转，请先恢复为模板渲染。</p>
      <n-space v-for="(rd, i) in redirects" :key="i" style="margin-bottom: 8px">
        <n-input v-model:value="rd.from_path" placeholder="来源路径" :disabled="rawMode" style="width: 160px" />
        <n-input v-model:value="rd.to_url" placeholder="目标 URL" :disabled="rawMode" style="width: 280px" />
        <n-input-number v-model:value="rd.code" :min="301" :max="308" :disabled="rawMode" />
        <n-button :disabled="rawMode" @click="redirects.splice(i, 1)">删除</n-button>
      </n-space>
      <n-space>
        <n-button :disabled="rawMode" @click="addRedirect">添加</n-button>
        <n-button type="primary" :disabled="rawMode" @click="saveRedirects">保存跳转</n-button>
      </n-space>
    </n-card>
    <n-card title="nginx conf 原文">
      <n-input v-model:value="conf" type="textarea" :autosize="{ minRows: 16 }" />
      <n-button style="margin-top: 8px" @click="saveRaw">保存原文</n-button>
    </n-card>
  </n-space>
</template>
