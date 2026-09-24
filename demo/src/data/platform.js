export const modules = [
  {
    id: "base",
    index: "1",
    name: "底座",
    summary: "人、角色、菜单和接口。",
  },
  {
    id: "tree",
    index: "2",
    name: "服务树",
    summary: "机器归属和写操作的权限从这里长出去。",
  },
  {
    id: "ticket",
    index: "3",
    name: "工单",
    summary: "先审批，再执行。模板本身没有状态。",
  },
  {
    id: "task",
    index: "4",
    name: "任务",
    summary: "选一批机器，下发同一条命令。",
  },
  {
    id: "monitor",
    index: "5",
    name: "监控",
    summary: "管采集和告警配置，不重做看图。",
  },
  {
    id: "k8s",
    index: "6",
    name: "集群",
    summary: "管理员看节点，应用运维看自己的实例。",
  },
  {
    id: "cicd",
    index: "7",
    name: "发布",
    summary: "开发环境直接发，生产走工单。",
  },
  {
    id: "db",
    index: "8",
    name: "数据库",
    summary: "登记叶子上的 MySQL。主从不造延迟，还原走工单。",
  },
];

export const tree = [
  {
    id: "arch",
    name: "基础架构",
    depth: 0,
    owners: ["周宁"],
  },
  {
    id: "observe",
    name: "可观测",
    depth: 1,
    owners: ["周宁", "许衡"],
    parentId: "arch",
  },
  {
    id: "edge",
    name: "入口",
    depth: 1,
    owners: ["周宁"],
    parentId: "arch",
  },
  {
    id: "trade",
    name: "交易",
    depth: 0,
    owners: ["林夏"],
  },
  {
    id: "order",
    name: "订单",
    depth: 1,
    owners: ["林夏", "陈舟"],
    parentId: "trade",
    leaf: true,
  },
  {
    id: "pay",
    name: "支付",
    depth: 1,
    owners: ["林夏"],
    parentId: "trade",
    leaf: true,
  },
];

const nodeName = Object.fromEntries(tree.map((node) => [node.id, node.name]));

export function labelOf(nodeId) {
  return nodeName[nodeId] || "全部节点";
}

export function visibleRecords(records, nodeId) {
  if (!nodeId) return records;
  const node = tree.find((item) => item.id === nodeId);
  if (!node) return records;
  const allowed = new Set([nodeId]);
  if (!node.leaf) {
    tree
      .filter((item) => item.parentId === nodeId || item.id === nodeId)
      .forEach((item) => allowed.add(item.id));
  }
  return records.filter((record) => allowed.has(record.nodeId));
}

export const users = [
  { name: "周宁", roles: "平台管理员、基础架构运维", menus: "八个模块" },
  { name: "林夏", roles: "交易运维", menus: "服务树、工单、发布、监控" },
  { name: "陈舟", roles: "订单研发", menus: "工单、发布" },
];

export const roles = [
  { name: "平台管理员", bind: "全部菜单和接口" },
  { name: "集群管理员", bind: "集群模块的节点操作" },
  { name: "应用运维", bind: "自己负责节点上的实例和发布" },
];

export const machines = [
  { name: "obs-a-01", ip: "10.4.1.21", nodeId: "observe", vendor: "自建", spec: "16C 64G" },
  { name: "obs-a-02", ip: "10.4.1.22", nodeId: "observe", vendor: "自建", spec: "16C 64G" },
  { name: "edge-b-03", ip: "10.4.8.11", nodeId: "edge", vendor: "公有云", spec: "8C 32G" },
  { name: "order-c-07", ip: "10.8.2.17", nodeId: "order", vendor: "公有云", spec: "8C 16G" },
  { name: "order-c-08", ip: "10.8.2.18", nodeId: "order", vendor: "公有云", spec: "8C 16G" },
  { name: "pay-c-02", ip: "10.8.3.9", nodeId: "pay", vendor: "公有云", spec: "8C 16G" },
];

export const tickets = [
  {
    id: "WO-1842",
    title: "订单库升配",
    nodeId: "order",
    owner: "陈舟",
    status: "pending_approve",
  },
  {
    id: "WO-1836",
    title: "支付证书轮换",
    nodeId: "pay",
    owner: "林夏",
    status: "pending_action",
  },
  {
    id: "WO-1828",
    title: "入口扩容被拒绝",
    nodeId: "edge",
    owner: "周宁",
    status: "reject",
  },
  {
    id: "WO-1811",
    title: "可观测节点初始化",
    nodeId: "observe",
    owner: "许衡",
    status: "finished",
  },
];

export const ticketStatus = {
  pending_approve: { text: "待审批", tone: "sky" },
  reject: { text: "已拒绝", tone: "blood" },
  pending_action: { text: "待执行", tone: "copper" },
  finished: { text: "已完成", tone: "pine" },
};

export const tasks = [
  {
    name: "核对时钟",
    nodeId: "observe",
    batch: "每轮 2 台",
    state: "执行中",
    progress: "1 / 2",
  },
  {
    name: "内核参数基线",
    nodeId: "order",
    batch: "每轮 5 台",
    state: "已暂停",
    progress: "6 / 20",
  },
  {
    name: "磁盘只读巡检",
    nodeId: "pay",
    batch: "不限",
    state: "已完成",
    progress: "4 / 4",
  },
];

export const pools = [
  { name: "基础采集", nodes: "可观测", workers: "2 台", write: "远端存储" },
  { name: "交易采集", nodes: "订单、支付", workers: "2 台", write: "远端存储" },
];

export const jobs = [
  { name: "node-exporter", nodeId: "observe", mode: "服务树", target: "叶子节点 :9100" },
  { name: "订单进程", nodeId: "order", mode: "服务树", target: "叶子节点 :9256" },
  { name: "支付进程", nodeId: "pay", mode: "服务树", target: "叶子节点 :9256" },
];

export const alerts = [
  { name: "订单错误率", nodeId: "order", group: "交易值班", level: "紧急", state: "触发中" },
  { name: "入口 5xx", nodeId: "edge", group: "基础架构值班", level: "警告", state: "已认领" },
  { name: "采集目标失联", nodeId: "observe", group: "可观测值班", level: "警告", state: "静默到今晚" },
];

export const clusters = [
  { name: "prod-a", env: "生产", version: "1.29", health: "正常", kube: "已托管" },
  { name: "dev-a", env: "开发", version: "1.29", health: "正常", kube: "已托管" },
];

export const apps = [
  { name: "order-api", nodeId: "order", cluster: "prod-a", image: "order-api:1.8.3", replicas: "6" },
  { name: "pay-gateway", nodeId: "pay", cluster: "prod-a", image: "pay-gateway:2.1.0", replicas: "4" },
];

export const releases = [
  {
    id: "REL-77",
    name: "order-api",
    nodeId: "order",
    env: "生产",
    tag: "1.8.4",
    stage: "预发已通过，等待生产",
  },
  {
    id: "REL-76",
    name: "pay-gateway",
    nodeId: "pay",
    env: "开发",
    tag: "2.2.0-dev",
    stage: "已自动部署",
  },
];
