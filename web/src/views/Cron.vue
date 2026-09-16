<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NSpace, NSwitch, useMessage } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { api } from "../api";

const msg = useMessage();
const jobs = ref<
  { id: number; name: string; schedule: string; command: string; enabled: boolean; last_run_at: string | null; last_status: string }[]
>([]);
const drifted = ref(false);
const sch = ref<{ id: string; every: string; last_run: string; last_error: string }[]>([]);
const name = ref("");
const schedule = ref("*/5 * * * *");
const command = ref("/usr/bin/true");
const enabled = ref(true);

const cols: DataTableColumns<(typeof jobs.value)[0]> = [
  { title: "名称", key: "name" },
  { title: "调度", key: "schedule" },
  { title: "命令", key: "command", ellipsis: { tooltip: true } },
  { title: "启用", key: "enabled", render: (r) => (r.enabled ? "是" : "否") },
  { title: "上次", key: "last_status" },
];
const schCols: DataTableColumns<(typeof sch.value)[0]> = [
  { title: "Job", key: "id" },
  { title: "周期", key: "every" },
  { title: "上次运行", key: "last_run" },
  { title: "错误", key: "last_error" },
];

async function load() {
  const c = await api<{ items: typeof jobs.value; drifted: boolean }>("GET", "/cron");
  if (c.ok) {
    jobs.value = c.data.items || [];
    drifted.value = c.data.drifted;
  }
  const s = await api<{ items: typeof sch.value }>("GET", "/scheduler");
  if (s.ok) sch.value = s.data.items || [];
}

onMounted(load);

async function create() {
  const r = await api("POST", "/cron", {
    name: name.value,
    schedule: schedule.value,
    command: command.value,
    enabled: enabled.value,
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已写入 crontab");
  name.value = "";
  await load();
}

async function run(id: number) {
  const r = await api("POST", `/cron/${id}/run`, {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已执行");
  await load();
}

async function del(id: number) {
  const r = await api("DELETE", `/cron/${id}`, {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已删除");
  await load();
}

async function toggle(row: (typeof jobs.value)[0]) {
  const r = await api("PUT", `/cron/${row.id}`, {
    name: row.name,
    schedule: row.schedule,
    command: row.command,
    enabled: !row.enabled,
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  await load();
}
</script>

<template>
  <n-space vertical>
    <n-card title="内部调度器">
      <p>面板进程内任务，不写入 crontab。</p>
      <n-data-table :columns="schCols" :data="sch" />
    </n-card>
    <n-card title="用户 crontab（面板托管段）">
      <n-alert v-if="drifted" type="warning" title="与系统 crontab 不一致">
        系统 crontab 里面板标记段与库中记录不同。保存任意任务会按库重写标记段，标记外的条目保留。
      </n-alert>
      <n-form inline>
        <n-form-item label="名称"><n-input v-model:value="name" /></n-form-item>
        <n-form-item label="五段 cron"><n-input v-model:value="schedule" style="width: 160px" /></n-form-item>
        <n-form-item label="绝对路径命令"><n-input v-model:value="command" style="width: 280px" /></n-form-item>
        <n-form-item label="启用"><n-switch v-model:value="enabled" /></n-form-item>
        <n-button type="primary" @click="create">添加</n-button>
      </n-form>
      <n-data-table :columns="cols" :data="jobs" />
      <n-space style="margin-top: 8px">
        <n-button v-for="j in jobs" :key="j.id" size="small" @click="run(j.id)">立刻执行 {{ j.name }}</n-button>
        <n-button v-for="j in jobs" :key="'t' + j.id" size="small" @click="toggle(j)">{{ j.enabled ? "停用" : "启用" }} {{ j.name }}</n-button>
        <n-button v-for="j in jobs" :key="'d' + j.id" type="error" size="small" @click="del(j.id)">删除 {{ j.name }}</n-button>
      </n-space>
    </n-card>
  </n-space>
</template>
