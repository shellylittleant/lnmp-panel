<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NSpace, NDataTable, useMessage } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { api, apiForm, downloadSQL, waitJob } from "../api";

const msg = useMessage();
const router = useRouter();
const env = ref<any>(null);
const busy = ref("");
const dbs = ref<{ name: string; charset: string; collate: string }[]>([]);
const users = ref<{ id: number; user: string; host: string; databases: string[] }[]>([]);
const dbName = ref("");
const uName = ref("");
const uHost = ref("localhost");
const uPass = ref("");
const uDBs = ref("");
const cnf = ref<any>({});
const cnfRaw = ref("");
const drifted = ref(false);
const rawMode = ref(false);
const force = ref(false);
const impDB = ref("");
const fileEl = ref<HTMLInputElement | null>(null);

const dbCols: DataTableColumns<(typeof dbs.value)[0]> = [
  { title: "库名", key: "name" },
  { title: "字符集", key: "charset" },
  { title: "校对", key: "collate" },
];
const userCols: DataTableColumns<(typeof users.value)[0]> = [
  { title: "用户", key: "user" },
  { title: "Host", key: "host" },
  { title: "库", key: "databases", render: (r) => (r.databases || []).join(", ") },
];

async function load() {
  const e = await api<any>("GET", "/env");
  if (e.ok) env.value = e.data;
  const d = await api<{ items: typeof dbs.value }>("GET", "/mysql/databases");
  if (d.ok) dbs.value = d.data.items || [];
  const u = await api<{ items: typeof users.value }>("GET", "/mysql/users");
  if (u.ok) users.value = u.data.items || [];
  const c = await api<any>("GET", "/mysql/cnf");
  if (c.ok) {
    cnf.value = c.data.form || {};
    cnfRaw.value = c.data.content || "";
    drifted.value = c.data.drifted;
    rawMode.value = c.data.raw_mode;
  }
}

onMounted(load);

async function createDB() {
  const r = await api("POST", "/mysql/databases", { name: dbName.value, charset: "utf8mb4" });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已建库");
  dbName.value = "";
  await load();
}

async function dropDB(name: string) {
  if (!window.confirm("删除数据库 " + name + "：执行 DROP DATABASE，库内数据不可恢复。")) return;
  const r = await api("DELETE", "/mysql/databases/" + name);
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已删除");
  await load();
}

async function createUser() {
  const r = await api("POST", "/mysql/users", {
    user: uName.value,
    host: uHost.value,
    password: uPass.value,
    databases: uDBs.value.split(",").map((x) => x.trim()).filter(Boolean),
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已建用户");
  uPass.value = "";
  await load();
}

async function delUser(id: number, label: string) {
  if (!window.confirm("删除 MySQL 用户 " + label + "：该账号立即不能登录。")) return;
  const r = await api("DELETE", "/mysql/users/" + id);
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已删除用户");
  await load();
}

async function exp(name: string) {
  const r = await downloadSQL(`/mysql/databases/${name}/export`, name + ".sql");
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
}

const newPass = ref<Record<number, string>>({});

async function setPass(u: { id: number; user: string; host: string }) {
  const password = (newPass.value[u.id] || "").trim();
  if (!password) {
    msg.error("请填写新密码");
    return;
  }
  const r = await api("PUT", "/mysql/users/" + u.id + "/password", { password });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  newPass.value[u.id] = "";
  msg.success("已修改 " + u.user + "@" + u.host + " 的密码");
}

async function imp() {
  if (!impDB.value || !fileEl.value?.files?.length) {
    msg.error("请选择库和 SQL 文件");
    return;
  }
  if (!window.confirm("向数据库 " + impDB.value + " 导入 SQL：文件中的语句会立即执行，可能覆盖已有数据。")) return;
  const fd = new FormData();
  fd.append("file", fileEl.value.files[0]);
  busy.value = "导入";
  try {
    const r = await apiForm<{ job_id: string }>(`/mysql/databases/${impDB.value}/import`, fd);
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    await waitJob(r.data.job_id, (ph, d) => (busy.value = `导入：${ph} ${d}`));
    msg.success("导入完成");
  } catch (e: any) {
    msg.error(e.message);
  } finally {
    busy.value = "";
  }
}

async function saveCnf() {
  const r = await api("PUT", "/mysql/cnf", { ...cnf.value, force: force.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已保存，MySQL 已重启，配置在 1 到 2 秒内生效");
  await load();
}

async function saveCnfRaw() {
  const r = await api("PUT", "/mysql/cnf/raw", { content: cnfRaw.value, force: force.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("原文已保存");
  await load();
}

async function useTpl() {
  const r = await api("POST", "/mysql/cnf/use-template", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已恢复模板");
  await load();
}
</script>

<template>
  <n-space vertical>
    <n-alert v-if="env && !env.mysql_installed" type="warning" title="尚未安装 MySQL">
      请到设置 → 环境安装完成后再建库。
      <n-button size="small" style="margin-left: 8px" @click="router.push({ path: '/settings', query: { tab: 'env' } })">去环境安装</n-button>
    </n-alert>
    <n-card title="数据库">
      <n-form inline>
        <n-form-item label="库名"><n-input v-model:value="dbName" placeholder="blog" /></n-form-item>
        <n-button type="primary" @click="createDB">创建</n-button>
      </n-form>
      <n-data-table :columns="dbCols" :data="dbs" />
      <n-space style="margin-top: 8px">
        <n-input v-model:value="impDB" placeholder="导入/导出的库名" style="width: 160px" />
        <n-button @click="impDB && exp(impDB)">导出 SQL</n-button>
        <input ref="fileEl" type="file" accept=".sql,text/plain" />
        <n-button @click="imp">导入</n-button>
        <n-button type="error" :disabled="!impDB" @click="dropDB(impDB)">删除该库</n-button>
      </n-space>
    </n-card>
    <n-card title="用户">
      <n-form inline>
        <n-form-item label="用户"><n-input v-model:value="uName" /></n-form-item>
        <n-form-item label="Host"><n-input v-model:value="uHost" /></n-form-item>
        <n-form-item label="密码"><n-input type="password" v-model:value="uPass" /></n-form-item>
        <n-form-item label="授权库"><n-input v-model:value="uDBs" placeholder="blog,app" /></n-form-item>
        <n-button type="primary" @click="createUser">创建</n-button>
      </n-form>
      <n-data-table :columns="userCols" :data="users" />
      <n-space style="margin-top: 8px">
        <n-space v-for="u in users" :key="u.id">
          <span>{{ u.user }}@{{ u.host }}</span>
          <n-input v-model:value="newPass[u.id]" type="password" placeholder="新密码" style="width: 160px" />
          <n-button size="small" @click="setPass(u)">改密码</n-button>
          <n-button type="error" size="small" @click="delUser(u.id, u.user + '@' + u.host)">删除</n-button>
        </n-space>
      </n-space>
    </n-card>
    <n-card title="面板托管 my.cnf 片段">
      <n-alert v-if="drifted" type="warning" title="配置漂移">磁盘与面板上次写入不一致。</n-alert>
      <n-alert v-if="rawMode" type="info" title="原文模式">表单只读，请先恢复模板。</n-alert>
      <n-alert type="warning" title="保存会重启 MySQL">所有数据库连接会断开，请确认无进行中的导入后再保存。</n-alert>
      <n-form>
        <n-form-item label="max_connections"><n-input v-model:value="cnf.max_connections" :disabled="rawMode" /></n-form-item>
        <n-form-item label="innodb_buffer_pool_size"><n-input v-model:value="cnf.innodb_buffer_pool_size" :disabled="rawMode" /></n-form-item>
        <n-form-item label="slow_query_log"><n-input v-model:value="cnf.slow_query_log" :disabled="rawMode" /></n-form-item>
        <n-form-item label="long_query_time"><n-input v-model:value="cnf.long_query_time" :disabled="rawMode" /></n-form-item>
        <n-space>
          <n-button type="primary" :disabled="rawMode" @click="saveCnf">保存表单</n-button>
          <n-button @click="useTpl">恢复为模板渲染</n-button>
        </n-space>
      </n-form>
      <n-input v-model:value="cnfRaw" type="textarea" :autosize="{ minRows: 8 }" style="margin-top: 12px" />
      <n-space style="margin-top: 8px">
        <n-button @click="saveCnfRaw">保存原文</n-button>
        <label><input type="checkbox" v-model="force" /> 确认覆盖磁盘</label>
      </n-space>
    </n-card>
  </n-space>
</template>
