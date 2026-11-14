import {
  ActivityHeatmap,
  AreaChart,
  Button,
  Callout,
  Card,
  CardBody,
  CardHeader,
  ChartComparisonGrid,
  ChartContainer,
  CollapsibleSection,
  Dialog,
  Divider,
  Fluency,
  Grid,
  H1,
  H2,
  ImprovementDisclosure,
  ImprovementList,
  MetricsGrid,
  ReportSection,
  ReportShell,
  Row,
  SendToChatButton,
  SourceLocationsDisclosure,
  Stack,
  SwimlaneBubbleChart,
  Table,
  Tag,
  Text,
  canvasSemanticSpacing,
  useCanvasAction,
} from "qoder/canvas";
import { useState } from "react";
import canvasData from "./canvas.json";

const report = canvasData;

const taskLoopReaderCopyStyle = { maxWidth: 940 };

const DIMENSION_SUMMARY_EXAMPLE = "Example: project guidance makes the main workflow clear, but ownership for cross-cutting changes is not documented.";

function list(value) {
  return Array.isArray(value) ? value : [];
}

function clampScore(value) {
  const score = Number(value);
  if (!Number.isFinite(score)) return 0;
  return Math.max(0, Math.min(100, score));
}

function projectName() {
  return report.summary?.projectName ?? "Better Harness Report";
}

function textValue(value) {
  return typeof value === "string" ? value.trim() : "";
}

function openingStrengths() {
  const explicit = list(report.summary?.strengths).map(textValue).filter(Boolean);
  return explicit.length ? explicit.slice(0, 3) : ["Reviewed project signals are organized into dimensions and issue findings."];
}

function averageScore(dimensions) {
  if (dimensions.length === 0) return 0;
  return Math.round(dimensions.reduce((sum, row) => sum + clampScore(row.score), 0) / dimensions.length);
}

function scoreTone(score) {
  if (score >= 70) return "success";
  if (score >= 40) return "warning";
  return "danger";
}

function stageStatus(score) {
  if (score >= 70) return "high";
  if (score >= 40) return "medium";
  if (score > 0) return "low";
  return "blocked";
}

function fluencyReason(row) {
  return textValue(row?.summary) || taskLoopCopy(
    "No reviewed score explanation is available for this dimension.",
    "这个维度暂时没有经过复核的评分说明。",
  );
}

function dimensionFluencyStages(dimensions) {
  return dimensions.map((row) => {
    const score = clampScore(row.score);
    const usesGenericBand = row.id !== "learning-capture";
    return {
      id: row.id,
      name: taskLoopDimensionLabel(row.id),
      score,
      ...(usesGenericBand ? { status: stageStatus(score), blocker: score <= 20 } : {}),
    };
  });
}

function dimensionFluencyTooltip(row) {
  return {
    content: fluencyReason(row),
  };
}

function severityTone(value) {
  if (value === "Critical" || value === "High") return "danger";
  if (value === "Medium") return "warning";
  if (value === "Low") return "success";
  return "neutral";
}

function severityRank(value) {
  if (value === "Critical") return 0;
  if (value === "High") return 1;
  if (value === "Medium") return 2;
  if (value === "Low") return 3;
  return 4;
}

function dimensionLabel(id, dimensions) {
  const match = dimensions.find((row) => row.id === id);
  return match?.label ?? id.replace(/-/g, " ");
}

function aiAgentPractice() {
  return report.summary?.aiAgentPractice ?? {};
}

function practiceRows() {
  const rows = aiAgentPractice().coverageRows;
  return Array.isArray(rows) ? rows : [];
}

function memoryPracticeDetails() {
  const details = aiAgentPractice().memoryDetails;
  return details && typeof details === "object" && !Array.isArray(details) ? details : null;
}

function repositorySourceRows() {
  const rows = aiAgentPractice().repositorySourceRows;
  return Array.isArray(rows) ? rows : [];
}

function inspectedSurfaces() {
  const surfaces = aiAgentPractice().inspectedSurfaces;
  return Array.isArray(surfaces) ? surfaces : [];
}

function visiblePracticePaths(value) {
  return list(value).map(textValue).filter((candidate) => candidate
    && !candidate.includes("SharedClientCache/projects/")
    && !candidate.startsWith("/")
    && !/^[A-Za-z]:[\\/]/.test(candidate)
    && !candidate.split(/[\\/]/).includes(".."));
}

function practiceDescription(surface) {
  const descriptions = {
    Rules: ["Standing project guidance and task-routing instructions.", "项目常驻指引与任务路由说明。"],
    Skills: ["Reusable agent workflows available to the project.", "项目可用的可复用 Agent 工作流。"],
    "Custom Agents": ["Specialized agent profiles available for delegated work.", "可用于委派工作的专用 Agent 配置。"],
    Hooks: ["Lifecycle automation around agent and delivery events.", "围绕 Agent 与交付事件的生命周期自动化。"],
    MCP: ["External tools and resources exposed through MCP.", "通过 MCP 暴露的外部工具与资源。"],
    Commands: ["Named command entry points for repeatable agent work.", "可重复 Agent 工作的命令入口。"],
    Workflows: ["Reusable multi-step project workflows.", "可复用的多步骤项目工作流。"],
    Plugins: ["Installed packages that contribute agent capabilities.", "提供 Agent 能力的已安装插件。"],
    "Session Insights": ["Task-session evidence available for report analysis.", "可用于报告分析的任务会话证据。"],
    Memories: ["Metadata-only project and global Memory inventory.", "仅含元数据的项目级与全局 Memory 盘点。"],
  };
  const copy = descriptions[surface] ?? ["Recorded agent capability sources.", "已记录的 Agent 能力来源。"];
  return taskLoopCopy(copy[0], copy[1]);
}

function TaskLoopPracticePaths({ paths }) {
  if (paths.length === 0) return null;
  const preview = paths.slice(0, 2);
  const remaining = paths.slice(2);
  return (
    <Stack gap={4}>
      {preview.map((path, index) => (
        <Text key={`${path}-${index}`} size="sm" tone="tertiary" truncate>{path}</Text>
      ))}
      {remaining.length ? (
        <CollapsibleSection
          size="sm"
          defaultOpen={false}
          title={(
            <Text size="sm" weight="semibold">
              {taskLoopCopy(`View ${remaining.length} more locations`, `查看其余 ${remaining.length} 个位置`)}
            </Text>
          )}
          bodyStyle={{ padding: "4px 0 0 16px" }}
          headerStyle={{ borderBottom: "none", minHeight: 24 }}
        >
          <Stack gap={4} style={{ maxHeight: 240, overflowY: "auto", paddingRight: 4 }}>
            {remaining.map((path, index) => (
              <Text key={`${path}-${index}`} size="sm" tone="tertiary" style={{ overflowWrap: "anywhere" }}>{path}</Text>
            ))}
          </Stack>
        </CollapsibleSection>
      ) : null}
    </Stack>
  );
}

function PracticeSourceCard({ row }) {
  const paths = visiblePracticePaths(row.paths);
  const scopes = list(row.scopes);
  return (
    <Card size="sm">
      <CardHeader
        title={(
          <Row gap={7} align="center" style={{ minWidth: 0 }}>
            <PracticeSurfaceIcon row={row} />
            <Text weight="semibold" truncate>{row.surface ?? taskLoopCopy("Surface", "能力面")}</Text>
          </Row>
        )}
        trailing={Number.isInteger(Number(row.count)) ? <Tag size="sm" tone="neutral">{row.count}</Tag> : undefined}
      />
      <CardBody style={{ height: "100%" }}>
        <Stack gap={10} style={{ height: "100%" }}>
          <Text size="sm" tone="secondary" style={{ lineHeight: 1.5 }}>{practiceDescription(row.surface)}</Text>
          {scopes.length || paths.length ? (
            <Stack gap={6} style={{ marginTop: "auto" }}>
              {scopes.length ? (
                <Row gap={5} wrap>
                  {scopes.map((scope) => <Tag key={scope} size="sm" tone="info">{scope}</Tag>)}
                </Row>
              ) : null}
              {paths.length ? (
                <Stack gap={4}>
                  <Text size="sm" weight="semibold" tone="tertiary">{taskLoopCopy("Sources", "来源")}</Text>
                  <TaskLoopPracticePaths paths={paths} />
                </Stack>
              ) : null}
            </Stack>
          ) : null}
        </Stack>
      </CardBody>
    </Card>
  );
}

function RepositorySourceCard({ row }) {
  const paths = visiblePracticePaths(row.paths);
  return (
    <Card>
      <CardBody style={{ height: "100%", padding: 16 }}>
        <Stack gap={12} style={{ height: "100%" }}>
          <Row justify="space-between" align="center" gap={12}>
            <Text weight="semibold" truncate>{row.surface ?? taskLoopCopy("Surface", "能力面")}</Text>
            <Tag size="sm" tone="neutral">{Number(row.count ?? 0)}</Tag>
          </Row>
          <Row gap={5} wrap>
            <Tag size="sm" tone="info">{textValue(row.provider) || taskLoopCopy("unknown provider", "未知 provider")}</Tag>
            <Tag size="sm" tone="neutral">{textValue(row.owner) || "."}</Tag>
          </Row>
          <Text size="sm" tone="secondary" style={{ lineHeight: 1.5 }}>
            {taskLoopCopy(
              "Repository-declared source; existence alone does not add it to the selected provider's active count.",
              "仓库声明的源码资产；仅凭文件存在不会计入当前 provider 的活跃数量。",
            )}
          </Text>
          <TaskLoopPracticePaths paths={paths} />
        </Stack>
      </CardBody>
    </Card>
  );
}

function OpeningStrengths() {
  const strengths = openingStrengths();
  return (
    <Callout tone="success" title="What is working">
      <Stack gap={6}>
        {strengths.map((strength, index) => (
          <Text key={index} size="sm">{strength}</Text>
        ))}
      </Stack>
    </Callout>
  );
}

function DimensionSummary({ dimensions }) {
  return (
    <Stack gap={8}>
      {dimensions.map((row) => {
        const score = clampScore(row.score);
        return (
          <Card key={row.id} size="sm">
            <CardHeader
              title={<Text weight="semibold">{taskLoopDimensionLabel(row.id)}</Text>}
              trailing={<Tag tone={scoreTone(score)}>{score}%</Tag>}
            />
            <CardBody>
              <Stack gap={4}>
                <Text size="sm" tone="secondary">{textValue(row.summary) || DIMENSION_SUMMARY_EXAMPLE}</Text>
                {list(row.findingRefs).length ? (
                  <Text size="sm" tone="secondary">Linked findings: {row.findingRefs.join(", ")}</Text>
                ) : null}
              </Stack>
            </CardBody>
          </Card>
        );
      })}
    </Stack>
  );
}

function FindingItem({ row, dimensions }) {
  return (
    <Card size="sm">
      <CardHeader
        title={<Text weight="semibold">{row.title ?? row.id}</Text>}
        trailing={<Tag tone={severityTone(row.severity)}>{row.severity ?? "Unrated"}</Tag>}
      />
      <CardBody>
        <Stack gap={8}>
          <Row gap={8} align="center" wrap>
            {list(row.dimensionRefs).slice(0, 1).map((ref) => (
              <Tag key={ref} tone="neutral">{dimensionLabel(ref, dimensions)}</Tag>
            ))}
          </Row>
          <Row gap={12} align="center" justify="end" wrap>
            <SendToChatButton text={row.aiFixPrompt} options={{ submit: false }} disabled={!row.aiFixPrompt}>
              AI Fix
            </SendToChatButton>
          </Row>
        </Stack>
      </CardBody>
    </Card>
  );
}

function PracticeCoverage() {
  const rows = practiceRows();
  const surfaces = inspectedSurfaces();

  return (
    <Stack gap={8}>
      <Row justify="space-between" align="center" wrap gap={8}>
        <Text weight="semibold">AI Agent Practices</Text>
        {surfaces.length ? <Tag tone="neutral">{surfaces.length} surfaces</Tag> : null}
      </Row>
      {rows.length ? (
        <Grid columns={3} minColumnWidth={260} gap={10} align="stretch">
          {rows.map((row, index) => <PracticeSourceCard key={row.surface ?? index} row={row} />)}
        </Grid>
      ) : <Text tone="secondary">No AI Agent practice rows recorded.</Text>}
    </Stack>
  );
}

function usesChineseReaderCopy() {
  const locale = textValue(report.summary?.locale);
  if (locale) return locale.toLowerCase().startsWith("zh");
  const readerSample = [
    ...list(report.summary?.strengths),
    ...list(report.findings).slice(0, 3).flatMap((row) => [row?.title, row?.reason, row?.reader]),
  ].map(textValue).join(" ");
  return /[\u3400-\u9fff]/.test(readerSample);
}

function taskLoopCopy(english, chinese) {
  return usesChineseReaderCopy() ? chinese : english;
}

function taskLoopDimensionLabel(id) {
  if (!id) return taskLoopCopy("not observed", "未观察到");
  return dimensionLabel(id, list(report.summary?.dimensions));
}

function learningStateLabel(value) {
  const labels = {
    "N/A": ["Needs a comparison", "需要比较"],
    pending: ["Comparison planned", "已计划比较"],
    improving: ["Improving", "正在改善"],
    unchanged: ["No clear change", "没有明显变化"],
    regressing: ["Worse — stop or revert", "变差——停止或回退"],
    "outcome-supported": ["A later result supports it", "后续结果支持它"],
  }[value];
  return labels ? taskLoopCopy(labels[0], labels[1]) : taskLoopCopy("Not observed", "未观察到");
}

function taskLoopSummary() {
  return report.summary?.atAGlance ?? {};
}

function taskLoopUsageActivity() {
  const activity = report.summary?.usageActivity;
  return activity && list(activity.dates).length ? activity : null;
}

function taskLoopUsageEfficiency() {
  const usage = report.summary?.usageEfficiency;
  if (!usage || typeof usage !== "object" || Array.isArray(usage)) return null;
  return usage.selection || usage.accounting || usage.longSessions || usage.modelUsage || usage.reviewLead
    ? usage
    : null;
}

function usageSeriesTotal(series) {
  return list(series).reduce((sum, row) => sum + Number(row?.total ?? 0), 0);
}

function usageActivityMatrix(activity, windowIndex = 0) {
  const sourceDates = list(activity?.dates);
  if (!sourceDates.length) return null;
  const latest = new Date(`${sourceDates.at(-1)}T00:00:00.000Z`);
  const first = new Date(`${sourceDates[0]}T00:00:00.000Z`);
  const windowDays = 183;
  const maxWindowIndex = Math.floor((latest.getTime() - first.getTime()) / (windowDays * 86_400_000));
  const safeWindowIndex = Math.min(Math.max(0, windowIndex), maxWindowIndex);
  const windowEnd = new Date(latest.getTime() - (safeWindowIndex * windowDays * 86_400_000));
  const windowStart = new Date(windowEnd.getTime() - ((windowDays - 1) * 86_400_000));
  const start = new Date(windowStart);
  start.setUTCDate(start.getUTCDate() - start.getUTCDay());
  const weekCount = Math.floor((windowEnd.getTime() - start.getTime()) / (7 * 86_400_000)) + 1;
  const columnGroups = [];
  Array.from({ length: weekCount }, (_, index) => {
    const date = new Date(start.getTime() + (index * 7 * 86_400_000));
    const label = usesChineseReaderCopy()
      ? `${date.getUTCMonth() + 1}月`
      : date.toLocaleDateString("en-US", { month: "short", timeZone: "UTC" });
    const previous = columnGroups.at(-1);
    if (previous?.month === date.getUTCMonth()) {
      previous.span += 1;
    } else {
      columnGroups.push({ label, start: index, span: 1, month: date.getUTCMonth() });
    }
  });
  const values = [];
  sourceDates.forEach((date, index) => {
    const parsed = new Date(`${date}T00:00:00.000Z`);
    if (parsed < windowStart || parsed > windowEnd) return;
    const column = Math.floor((parsed.getTime() - start.getTime()) / (7 * 86_400_000));
    if (column < 0 || column >= weekCount) return;
    const active = Number(activity?.sessions?.activeMinutes?.[index] ?? 0);
    values.push({
      row: parsed.getUTCDay(),
      column,
      value: active,
      ariaLabel: taskLoopCopy(`${date}: ${formatActivityMinutes(active)}`, `${date}：${formatActivityMinutes(active)}`),
    });
  });
  return {
    columns: weekCount,
    columnGroups: columnGroups.map(({ month, ...group }) => group),
    values,
    start: windowStart.toISOString().slice(0, 10),
    end: windowEnd.toISOString().slice(0, 10),
    windowIndex: safeWindowIndex,
    maxWindowIndex,
  };
}

function usageChartWindow(activity) {
  const dates = list(activity?.dates);
  const offset = Math.max(0, dates.length - 30);
  return { offset, categories: dates.slice(offset).map((date) => String(date).slice(5)) };
}

function visibleUsageSeries(series, offset, limit = 5) {
  const rows = list(series);
  const named = rows.filter((row) => row?.name !== "Other");
  const primary = named.slice(0, limit).map((row) => ({ name: usageSeriesLabel(row.name), data: list(row.daily).slice(offset), total: Number(row.total ?? 0) }));
  const remainder = [...named.slice(limit), ...rows.filter((row) => row?.name === "Other")];
  if (remainder.length > 0) {
    const length = primary[0]?.data.length ?? list(remainder[0]?.daily).slice(offset).length;
    primary.push({
      name: "Other",
      total: remainder.reduce((sum, row) => sum + Number(row?.total ?? 0), 0),
      data: Array.from({ length }, (_, index) => remainder.reduce((sum, row) => sum + Number(list(row?.daily).slice(offset)[index] ?? 0), 0)),
    });
  }
  return primary;
}

function formatUsageNumber(value) {
  return Math.round(Number(value ?? 0)).toLocaleString(usesChineseReaderCopy() ? "zh-CN" : "en-US");
}

function usageSeriesLabel(value) {
  if (value === "Unknown model") return taskLoopCopy("Unattributed model", "未归属模型");
  if (value === "Unknown Skill") return taskLoopCopy("Unattributed Skill", "未归属 Skill");
  return value;
}

function formatActivityMinutes(value) {
  const formatted = Number(value ?? 0).toLocaleString(usesChineseReaderCopy() ? "zh-CN" : "en-US", {
    maximumFractionDigits: 1,
  });
  return `${formatted} ${taskLoopCopy("min", "分钟")}`;
}

function taskLoopToolCallDurationMs(call) {
  const durationMs = Number(call?.durationMs);
  return call?.durationStatus === "observed" && Number.isFinite(durationMs) && durationMs >= 0
    ? durationMs
    : null;
}

function formatToolCallLatency(value) {
  const durationMs = Math.max(0, Number(value ?? 0));
  if (durationMs < 1_000) return `${Math.round(durationMs)} ms`;
  if (durationMs < 60_000) return `${Number((durationMs / 1_000).toFixed(durationMs < 10_000 ? 1 : 0))} s`;
  const totalSeconds = Math.round(durationMs / 1_000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return seconds > 0 ? `${minutes} min ${seconds} s` : `${minutes} min`;
}

function taskLoopCoverage() {
  return taskLoopSummary().coverage ?? {};
}

function confidenceLabel(value) {
  const normalized = textValue(value).toLowerCase();
  const labels = {
    high: ["High confidence", "高可信度"],
    medium: ["Medium confidence", "中等可信度"],
    low: ["Low confidence", "低可信度"],
  }[normalized];
  return labels ? taskLoopCopy(labels[0], labels[1]) : taskLoopCopy("Confidence not recorded", "未记录可信度");
}

function confidenceTone(value) {
  const normalized = textValue(value).toLowerCase();
  if (normalized === "high") return "success";
  if (normalized === "medium") return "warning";
  return "neutral";
}

function taskLoopStateLabel(value) {
  const labels = {
    Wired: ["Wired", "机制已接入"],
    Present: ["Present", "已发现机制"],
    Unobserved: ["Unobserved", "未观察到"],
    observed: ["Observed", "已观察到"],
    "Not applicable": ["Not applicable", "暂不适用"],
    "N/A": ["Needs a comparison", "需要比较"],
  }[value];
  return labels ? taskLoopCopy(labels[0], labels[1]) : textValue(value) || "—";
}

function taskLoopSubdimensionLabel(id) {
  for (const dimension of list(report.summary?.dimensions)) {
    const match = list(dimension?.subdimensions).find((row) => row?.id === id);
    if (match) return match.label ?? id;
  }
  return id;
}

function evidenceReferenceLabel(item) {
  return textValue(item?.label) || textValue(item?.id) || taskLoopCopy("Unnamed evidence", "未命名证据");
}

function evidenceReferenceMeta(item) {
  return [
    textValue(item?.status),
    textValue(item?.type),
    Number.isFinite(Number(item?.line)) ? `${taskLoopCopy("line", "行")} ${item.line}` : "",
  ].filter(Boolean).join(" · ");
}

function EvidenceReferenceList({ items }) {
  return (
    <Stack gap={7} style={{ maxHeight: 260, overflowY: "auto", paddingRight: 4 }}>
      {items.map((item, index) => (
        <Stack key={`${item?.group ?? item?.kind}-${item?.id ?? index}`} gap={3}>
          <Row gap={6} align="center" wrap>
            {item?.group ? <Tag size="sm" tone="neutral">{item.group}</Tag> : null}
            {item?.kind ? <Tag size="sm" tone="info">{item.kind}</Tag> : null}
            <Text size="sm" tone="secondary" style={{ overflowWrap: "anywhere" }}>{evidenceReferenceLabel(item)}</Text>
          </Row>
          {evidenceReferenceMeta(item) ? <Text size="sm" tone="tertiary">{evidenceReferenceMeta(item)}</Text> : null}
        </Stack>
      ))}
    </Stack>
  );
}

function severityLabel(value) {
  const labels = {
    Critical: ["Critical", "紧急"],
    High: ["High", "高"],
    Medium: ["Medium", "中"],
    Low: ["Low", "低"],
  }[value];
  return labels ? taskLoopCopy(labels[0], labels[1]) : value ?? "—";
}

function practiceSurfaceGlyph(surface) {
  return ({ Rules: "R", Skills: "S", "Custom Agents": "A", Hooks: "H", MCP: "M" })[surface] ?? textValue(surface).slice(0, 1).toUpperCase() ?? "?";
}

function PracticeSurfaceIcon({ row }) {
  return <Tag size="sm" tone="neutral">{practiceSurfaceGlyph(row?.surface)}</Tag>;
}

function TaskLoopReportHeader({ findings }) {
  const sources = practiceRows().filter((row) => Number(row?.count) > 0);
  const overview = textValue(report.summary?.overview);
  return (
    <Stack gap={8}>
      <H1>{projectName()}</H1>
      {overview ? <Text tone="secondary" style={{ ...taskLoopReaderCopyStyle, lineHeight: 1.5 }}>{overview}</Text> : null}
      <Row gap={10} align="center" wrap>
        <Text size="sm" tone="secondary">
          {taskLoopCopy(`${findings.length} prioritized improvements`, `${findings.length} 项优先优化`)}
        </Text>
        {sources.length ? (
          <Text size="sm" tone="secondary">
            {taskLoopCopy(`${sources.length} practice source types`, `${sources.length} 类实践来源`)}
          </Text>
        ) : null}
      </Row>
    </Stack>
  );
}

function TaskLoopFluency({ dimensions }) {
  if (dimensions.length === 0) return null;
  return (
    <Stack style={{ maxHeight: 200, overflow: "hidden" }}>
      <Fluency
        projectName={projectName()}
        stages={dimensionFluencyStages(dimensions)}
        tooltip={(_stage, index) => dimensionFluencyTooltip(dimensions[index])}
        height={180}
        highThreshold={70}
        mediumThreshold={40}
        showStageLabels
      />
    </Stack>
  );
}

function practiceCount(row) {
  const value = Number(row?.count);
  return Number.isInteger(value) ? value : "—";
}

function practiceScopeCell(row) {
  const scopes = list(row?.scopes).map(textValue).filter(Boolean);
  if (!scopes.length) return <Text size="sm" tone="tertiary">—</Text>;
  return <Text size="sm" tone="tertiary">{scopes.join(" · ")}</Text>;
}

function practiceSourceCell(row) {
  const memoryDetails = row?.surface === "Memories" ? memoryPracticeDetails() : null;
  if (memoryDetails) {
    const scopeSummary = list(memoryDetails.scopeCounts)
      .map((item) => `${textValue(item?.scope)} ${Number(item?.count ?? 0)}`)
      .filter(Boolean)
      .join(" · ");
    return <Text size="sm" tone="secondary">{scopeSummary || taskLoopCopy("Metadata scan complete", "元数据扫描完成")}</Text>;
  }
  const [firstPath] = visiblePracticePaths(row?.paths);
  return firstPath
    ? <Text size="sm" tone="secondary" style={{ overflowWrap: "anywhere" }}>{firstPath}</Text>
    : <Text size="sm" tone="tertiary">{taskLoopCopy("No source location recorded", "未记录来源位置")}</Text>;
}

function practiceSourceDetail(row) {
  const memoryDetails = row?.surface === "Memories" ? memoryPracticeDetails() : null;
  if (memoryDetails) {
    const integrity = memoryDetails.integrity ?? {};
    const integrityReviewed = integrity.status === "reviewed";
    return (
      <Stack gap={8} style={{ padding: "8px 4px 4px 16px" }}>
        <Text size="sm" weight="semibold">{taskLoopCopy("Memory inventory details", "Memory 盘点明细")}</Text>
        <Row gap={6} wrap>
          {list(memoryDetails.scopeCounts).map((item) => (
            <Tag key={item.scope} size="sm" tone="info">{item.scope}: {Number(item.count ?? 0)}</Tag>
          ))}
          <Tag size="sm" tone="neutral">{memoryDetails.scanState}</Tag>
        </Row>
        <Text size="sm" tone="secondary">
          {integrityReviewed
            ? taskLoopCopy(
                `Reviewed ${Number(integrity.reviewedTitleCount ?? 0)}/${Number(integrity.titleCount ?? 0)} titles; ${Number(integrity.exactCollisionGroups ?? 0)} exact-collision groups; ${Number(integrity.similarityCandidates ?? 0)} near-title candidates; ${integrity.truncated ? "review truncated" : "review complete"}.`,
                `已检查 ${Number(integrity.reviewedTitleCount ?? 0)}/${Number(integrity.titleCount ?? 0)} 个标题；同名碰撞组 ${Number(integrity.exactCollisionGroups ?? 0)}；相似标题候选 ${Number(integrity.similarityCandidates ?? 0)}；${integrity.truncated ? "检查已截断" : "检查完整"}。`,
              )
            : taskLoopCopy("Title integrity review unavailable.", "标题完整性检查不可用。")}
        </Text>
        <Text size="sm" tone="tertiary">
          {taskLoopCopy(
            "Metadata only. Memory titles, paths, and bodies are intentionally omitted.",
            "仅使用元数据；Memory 标题、路径和正文均有意省略。",
          )}
        </Text>
      </Stack>
    );
  }
  const paths = visiblePracticePaths(row?.paths);
  const remaining = paths.slice(1);
  const declaredCount = Number(row?.count);
  const unsuppliedCount = Number.isInteger(declaredCount)
    ? Math.max(0, declaredCount - paths.length)
    : 0;
  const additionalCount = remaining.length + unsuppliedCount;
  if (!additionalCount) return null;
  const unsuppliedMessage = unsuppliedCount
    ? taskLoopCopy(
        `${unsuppliedCount} additional source ${unsuppliedCount === 1 ? "location was" : "locations were"} counted, but the paths were not supplied to this report.`,
        `另有 ${unsuppliedCount} 个来源已计入总数，但其路径未随本报告提供。`,
      )
    : null;
  return (
    <SourceLocationsDisclosure
      locations={unsuppliedMessage ? [...remaining, unsuppliedMessage] : remaining}
      defaultOpen={false}
      label={(
        <Text size="sm" weight="semibold">
          {taskLoopCopy(
            `View ${additionalCount} more ${additionalCount === 1 ? "location" : "locations"}`,
            `查看其余 ${additionalCount} 个来源位置`,
          )}
        </Text>
      )}
      style={{ marginLeft: 0, paddingLeft: 0, borderLeft: "none" }}
    />
  );
}

function taskLoopPracticeColumns() {
  return [
    {
      key: "surface",
      title: taskLoopCopy("Asset", "资产"),
      role: "label",
      render: (row) => (
        <Stack gap={3}>
          <Text size="sm" weight="semibold">{row.surface ?? taskLoopCopy("Surface", "能力面")}</Text>
          <Text size="sm" tone="secondary">{practiceDescription(row.surface)}</Text>
        </Stack>
      ),
    },
    {
      key: "coverage",
      title: taskLoopCopy("Coverage", "覆盖范围"),
      role: "metric",
      render: (row) => (
        <Stack gap={4}>
          <Text size="sm" weight="semibold">
            {taskLoopCopy(`${practiceCount(row)} sources`, `${practiceCount(row)} 个来源`)}
          </Text>
          {practiceScopeCell(row)}
        </Stack>
      ),
    },
    {
      key: "source",
      title: taskLoopCopy("Representative source", "代表来源"),
      role: "description",
      render: practiceSourceCell,
    },
  ];
}

function TaskLoopPracticeTable({ rows = practiceRows() }) {
  if (rows.length === 0) {
    return <Text tone="secondary">{taskLoopCopy("No Agent assets recorded.", "未记录 Agent 工程资产。")}</Text>;
  }
  return (
    <Table
      columns={taskLoopPracticeColumns()}
      rows={rows}
      rowKey={(row) => row.surface ?? "surface"}
      density="default"
      resizable={false}
      renderDetail={practiceSourceDetail}
      emptyText={taskLoopCopy("No Agent asset coverage recorded", "未记录 Agent 工程资产覆盖")}
    />
  );
}

function TaskLoopActivityHeatmap({ activity }) {
  const [windowIndex, setWindowIndex] = useState(0);
  const matrix = usageActivityMatrix(activity, windowIndex);
  if (!matrix) return <Text tone="secondary">{taskLoopCopy("No dated session activity was observed.", "没有观察到带日期的会话活动。")}</Text>;
  return (
    <Stack gap={12}>
      <Row justify="space-between" align="center" wrap gap={8}>
        <Text size="sm" weight="semibold">{taskLoopCopy("Daily activity (active minutes)", "每日活动（活跃分钟）")}</Text>
        <Row gap={4} align="center" wrap style={{ width: "auto", marginLeft: "auto" }}>
          {matrix.maxWindowIndex > 0 ? (
            <Button
              size="sm"
              variant="text"
              disabled={matrix.windowIndex >= matrix.maxWindowIndex}
              onClick={() => setWindowIndex((value) => Math.min(value + 1, matrix.maxWindowIndex))}
            >
              {taskLoopCopy("Previous period", "上一时段")}
            </Button>
          ) : null}
          <Text size="sm" tone="tertiary">{matrix.start} — {matrix.end}</Text>
          {matrix.maxWindowIndex > 0 ? (
            <Button
              size="sm"
              variant="text"
              disabled={matrix.windowIndex === 0}
              onClick={() => setWindowIndex((value) => Math.max(0, value - 1))}
            >
              {taskLoopCopy("Next period", "下一时段")}
            </Button>
          ) : null}
        </Row>
      </Row>
      <ActivityHeatmap
        rows={taskLoopCopy(["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"], ["日", "一", "二", "三", "四", "五", "六"])}
        columns={matrix.columns}
        columnGroups={matrix.columnGroups}
        values={matrix.values}
        valueFormatter={(value) => String(Math.round(value))}
        valueSuffix={taskLoopCopy("active minutes", "活跃分钟")}
        emptyLabel={taskLoopCopy("no observed activity", "未观察到活动")}
        lessLabel={taskLoopCopy("Less", "少")}
        moreLabel={taskLoopCopy("More", "多")}
        ariaLabel={taskLoopCopy("Daily session activity", "每日会话活动")}
      />
    </Stack>
  );
}

function UsageStatRow({ label, value }) {
  if (value === undefined || value === null || value === "") return null;
  return (
    <Row justify="space-between" align="start" gap={12} style={{ minWidth: 0 }}>
      <Text size="sm" tone="secondary" style={{ flexShrink: 0 }}>{label}</Text>
      <Text size="sm" weight="semibold" style={{ minWidth: 0, overflowWrap: "anywhere", textAlign: "right" }}>{value}</Text>
    </Row>
  );
}

function UsageValueList({ series, limit = 5 }) {
  const rows = list(series).slice(0, limit);
  if (!rows.length) return <Text size="sm" tone="secondary">{taskLoopCopy("No usage observed.", "未观察到用量。")}</Text>;
  return (
    <Stack gap={7}>
      {rows.map((row, index) => (
        <Row key={`${row.name}-${index}`} justify="space-between" align="center" gap={12}>
          <Text size="sm" tone="secondary" truncate>{usageSeriesLabel(row.name)}</Text>
          <Text size="sm" weight="semibold" style={{ textAlign: "right" }}>{formatUsageNumber(row.total)}</Text>
        </Row>
      ))}
    </Stack>
  );
}

function taskLoopModelUsageColumns() {
  return [
    {
      key: "model",
      title: taskLoopCopy("Model", "模型"),
      minWidth: "180px",
      render: (row) => <Text size="sm" weight="semibold">{usageSeriesLabel(row.model)}</Text>,
    },
    {
      key: "responseCount",
      title: taskLoopCopy("Responses", "响应数"),
      width: "110px",
      align: "right",
      render: (row) => <Text size="sm">{formatUsageNumber(row.responseCount)}</Text>,
    },
    {
      key: "usageFieldObservedCount",
      title: taskLoopCopy("Usage fields observed", "观察到用量字段"),
      minWidth: "160px",
      align: "right",
      render: (row) => <Text size="sm">{formatUsageNumber(row.usageFieldObservedCount)}</Text>,
    },
    {
      key: "nonZeroUsageCount",
      title: taskLoopCopy("Non-zero usage", "非零用量记录"),
      minWidth: "140px",
      align: "right",
      render: (row) => <Text size="sm">{formatUsageNumber(row.nonZeroUsageCount)}</Text>,
    },
  ];
}

function TaskLoopModelUsageTable({ rows }) {
  if (!rows.length) return null;
  return (
    <Stack gap={7}>
      <Row justify="space-between" align="center" wrap gap={8}>
        <Text weight="semibold">{taskLoopCopy("Model response accounting", "模型响应明细")}</Text>
        <Tag tone="neutral">{rows.length} {taskLoopCopy("models", "个模型")}</Tag>
      </Row>
      <Text size="sm" tone="tertiary">
        {taskLoopCopy(
          "These are response counts, not model-active session counts or a quality comparison.",
          "这里统计的是响应次数，不是模型活跃会话数，也不代表模型质量对比。",
        )}
      </Text>
      <Table
        columns={taskLoopModelUsageColumns()}
        rows={rows}
        rowKey={(row) => row.model}
        density="compact"
      />
    </Stack>
  );
}

function TaskLoopLongSessionReview({ usage }) {
  const dispatchCanvasAction = useCanvasAction();
  const lead = usage?.reviewLead;
  const samples = list(usage?.longSessions?.samples);
  if (!lead || !samples.length) return null;
  const estimate = usage.longSessions?.estimate;
  const coverage = lead.sampleCoverage;
  const pendingCount = coverage?.shown ?? samples.length;
  const analyzedCount = usage.selection?.analyzedSessionCount ?? 0;
  const longestActiveMinutes = usage.longSessions?.longestActiveMinutes ?? Math.max(...samples.map((sample) => Number(sample.activeMinutes ?? 0)));
  return (
    <Stack gap={14}>
      <Row justify="space-between" align="start" wrap gap={12}>
        <Stack gap={5} style={{ flex: "1 1 620px", minWidth: 0 }}>
          <Row gap={8} align="center" wrap>
            <H2 style={{ fontSize: 18, lineHeight: 1.35 }}>{taskLoopCopy(`${pendingCount} long sessions need review`, `${pendingCount} 个长会话待复核`)}</H2>
            <Tag size="sm" tone="warning">{pendingCount} {taskLoopCopy("pending", "待复核")}</Tag>
          </Row>
          <Text size="sm" tone="secondary" style={taskLoopReaderCopyStyle}>
            {taskLoopCopy(
              `${pendingCount} of ${formatUsageNumber(analyzedCount)} analyzed sessions crossed the ${estimate?.activeThresholdMinutes ?? 45}-minute estimate threshold; the longest estimate is ${formatActivityMinutes(longestActiveMinutes)}. Treat them as investigation leads until reviewed.`,
              `${formatUsageNumber(analyzedCount)} 个已分析会话中有 ${pendingCount} 个超过 ${estimate?.activeThresholdMinutes ?? 45} 分钟估算阈值，最长估算为 ${formatActivityMinutes(longestActiveMinutes)}。在人工复核前，只将其视为调查线索。`,
            )}
          </Text>
        </Stack>
        <SendToChatButton text={lead.aiFixPrompt} options={{ submit: false }}>
          {taskLoopCopy(`Review ${pendingCount} sessions`, `复核 ${pendingCount} 个会话`)}
        </SendToChatButton>
      </Row>
      <Stack gap={14}>
        {samples.map((sample, sampleIndex) => {
          const failureCount = Number(sample.failureCount ?? 0);
          const trace = sample.toolTrace ?? {};
          const calls = list(trace.calls);
          const totalCalls = Math.max(Number(trace.totalCalls ?? 0), ...calls.map((call) => Number(call.step ?? 0)));
          const shownCalls = Number(trace.shownCalls ?? calls.length);
          const observedDurationCount = calls.filter((call) => taskLoopToolCallDurationMs(call) !== null).length;
          const chartTickEvery = taskLoopToolCallTickEvery(totalCalls);
          const chartMax = Math.max(1, totalCalls);
          const chartMinWidth = Math.max(760, chartMax * 12 + 220);
          const chartLanes = calls
            .map((call) => String(call.toolName ?? taskLoopCopy("Unknown tool", "未知工具")))
            .filter((toolName, index, all) => all.indexOf(toolName) === index)
            .map((toolName) => ({ id: toolName, label: toolName }));
          const chartData = calls.map((call) => {
            const durationMs = taskLoopToolCallDurationMs(call);
            return {
              id: `${sample.alias}-${call.id}`,
              laneId: String(call.toolName ?? taskLoopCopy("Unknown tool", "未知工具")),
              x: Number(call.step ?? 0),
              ...(durationMs === null ? {} : { value: durationMs }),
              label: String(call.toolName ?? taskLoopCopy("Unknown tool", "未知工具")),
              detail: call.status === "failed"
                ? taskLoopCopy(`Tool call ${formatUsageNumber(call.step)} failed`, `工具调用 ${formatUsageNumber(call.step)} 失败`)
                : durationMs === null
                  ? taskLoopCopy("Observed latency unavailable", "未观察到调用延迟")
                  : taskLoopCopy("Observed from tool use to result", "从工具调用到结果的观测延迟"),
              tone: call.status === "failed" ? "warning" : "neutral",
            };
          });
          return (
            <Stack key={sample.alias} gap={6}>
              {sampleIndex > 0 ? <Divider style={{ margin: "2px 0" }} /> : null}
              <Stack gap={5} style={{ minWidth: 0, padding: "6px 12px 0" }}>
                <Row justify="space-between" align="center" wrap gap={10}>
                  <Row gap={8} align="center" wrap style={{ minWidth: 0 }}>
                    <Text weight="semibold">{sample.alias}</Text>
                    <Tag size="sm" tone="neutral">{taskLoopLongSessionRoleLabel(sample.role)}</Tag>
                    <Text size="sm" tone="secondary">{formatActivityMinutes(sample.activeMinutes)}</Text>
                    <Tag size="sm" tone={failureCount > 0 ? "warning" : "neutral"}>
                      {formatUsageNumber(failureCount)} {taskLoopCopy("failures", "失败事件")}
                    </Tag>
                  </Row>
                  <Text size="sm" tone="tertiary" style={{ flexShrink: 0, marginLeft: "auto", whiteSpace: "nowrap" }}>
                    {taskLoopCopy(
                      `Showing ${formatUsageNumber(shownCalls)} of ${formatUsageNumber(totalCalls)} tool calls`,
                      `显示 ${formatUsageNumber(shownCalls)} / ${formatUsageNumber(totalCalls)} 个工具调用`,
                    )}
                  </Text>
                </Row>
                <Text size="sm" tone="secondary" style={{ minWidth: 0, overflowWrap: "anywhere" }}>
                  {taskLoopCopy("Request", "用户请求")}: {textValue(sample.userRequest) || taskLoopCopy("Unavailable after privacy filtering", "经隐私过滤后不可用")}
                </Text>
                {textValue(sample.sessionId) ? (
                  <Button
                    size="sm"
                    variant="text"
                    style={{ alignSelf: "flex-start", justifyContent: "flex-start", maxWidth: "100%", minWidth: 0, overflow: "hidden" }}
                    onClick={() => dispatchCanvasAction({
                      type: "aicoding.canvas.openQuestSession",
                      taskId: textValue(sample.sessionId),
                      sessionId: textValue(sample.sessionId),
                    })}
                  >
                    <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {taskLoopCopy("Session", "会话")}: {textValue(sample.sessionId)}
                    </span>
                  </Button>
                ) : null}
              </Stack>
              <CollapsibleSection
                size="sm"
                defaultOpen={false}
                title={<Text size="sm" weight="semibold">{taskLoopCopy("Tool-call chart", "工具调用图表")}</Text>}
                headerStyle={{ minHeight: 44 }}
                bodyStyle={{ padding: "8px 0 2px" }}
              >
                <Stack gap={6}>
                  <Stack gap={0} style={{ display: "block", maxWidth: "100%", overflowX: "auto", paddingBottom: 8 }}>
                    <Stack gap={0} style={{ display: "block", minWidth: chartMinWidth }}>
                      <SwimlaneBubbleChart
                        lanes={chartLanes}
                        data={chartData}
                        xMin={1}
                        xMax={chartMax}
                        xTickEvery={chartTickEvery}
                        xAxisLabel={taskLoopCopy("Tool-call step", "工具调用序号")}
                        xValueFormatter={(value) => formatUsageNumber(value)}
                        valueLabel={taskLoopCopy("Observed latency", "观测延迟")}
                        valueFormatter={formatToolCallLatency}
                        laneHeight={30}
                        minBubbleRadius={4}
                        maxBubbleRadius={10}
                        emptyLabel={taskLoopCopy("No tool calls observed for this session", "该会话未观察到工具调用")}
                        ariaLabel={taskLoopCopy(
                          `${sample.alias} tool calls by tool, sequence, and observed latency`,
                          `${sample.alias} 按工具、调用序号和观测延迟展示的工具调用`,
                        )}
                        style={{ overflow: "visible" }}
                      />
                    </Stack>
                  </Stack>
                  <Text size="sm" tone="tertiary">
                    {taskLoopCopy(
                      `Observed latency for ${formatUsageNumber(observedDurationCount)} of ${formatUsageNumber(shownCalls)} shown calls; bubble area scales with observed latency.`,
                      `已观察到 ${formatUsageNumber(observedDurationCount)} / ${formatUsageNumber(shownCalls)} 个调用的延迟；气泡面积随观测延迟变化。`,
                    )}
                  </Text>
                </Stack>
              </CollapsibleSection>
            </Stack>
          );
        })}
      </Stack>
      {estimate ? (
        <Text size="sm" tone="tertiary" style={taskLoopReaderCopyStyle}>
          {taskLoopCopy(
            `Estimate boundary: event gaps are capped at ${estimate.gapCapMinutes} minutes and gaps over ${estimate.idleGapMinutes} minutes are treated as idle.`,
            `估算边界：事件间隔最多计 ${estimate.gapCapMinutes} 分钟，超过 ${estimate.idleGapMinutes} 分钟按空闲处理。`,
          )}
        </Text>
      ) : null}
    </Stack>
  );
}

function taskLoopLongSessionRoleLabel(role) {
  if (role === "user-thread-candidate") return taskLoopCopy("Main-thread candidate", "主线程候选");
  if (role === "child-agent-candidate") return taskLoopCopy("Child-Agent candidate", "子 Agent 候选");
  return taskLoopCopy("Unknown candidate", "未知候选");
}

function taskLoopToolCallTickEvery(totalCalls) {
  const target = Math.max(1, Number(totalCalls ?? 0) / 8);
  return [1, 2, 5, 10, 20, 25, 50, 100].find((step) => step >= target)
    ?? Math.ceil(target / 100) * 100;
}

function TaskLoopProjectUsage({ activity, usage }) {
  if (!activity && !usage) return null;
  const activeMinutes = list(activity?.sessions?.activeMinutes).reduce((sum, value) => sum + Number(value ?? 0), 0);
  const census = usage?.selection;
  const longSessions = usage?.longSessions;
  const skillUses = usageSeriesTotal(activity?.skills);
  const analyzedSessions = census
    ? `${formatUsageNumber(census.analyzedSessionCount)} / ${formatUsageNumber(census.eligibleSessionCount)}`
    : activity ? formatUsageNumber(activity.sessions?.total) : null;
  return (
    <Stack gap={12}>
      {activity ? <TaskLoopActivityHeatmap activity={activity} /> : null}
      {activity ? <Divider style={{ margin: "2px 0" }} /> : null}
      <Grid columns={2} minColumnWidth={260} gap={canvasSemanticSpacing.section} align="start" divided>
        <Stack gap={9}>
          <Text weight="semibold">{taskLoopCopy("Activity insights", "使用概览")}</Text>
          <UsageStatRow label={taskLoopCopy("Sessions analyzed", "已分析会话")} value={analyzedSessions} />
          {activity ? <UsageStatRow label={taskLoopCopy("Estimated active minutes", "估算活跃分钟数")} value={formatUsageNumber(activeMinutes)} /> : null}
          {activity ? <UsageStatRow label={taskLoopCopy("Skills used", "Skill 使用")} value={formatUsageNumber(skillUses)} /> : null}
          {longSessions ? <UsageStatRow label={taskLoopCopy("Estimated active-long sessions", "估算活跃长会话")} value={formatUsageNumber(longSessions.activeCount)} /> : null}
        </Stack>
        <Stack gap={9}>
          <Text weight="semibold">{taskLoopCopy("Most used Skills", "最常使用的 Skills")}</Text>
          <UsageValueList series={activity?.skills} />
        </Stack>
      </Grid>
    </Stack>
  );
}

function TaskLoopUsageMethodology({ usage }) {
  if (!usage) return null;
  const census = usage.selection;
  const accounting = usage.accounting;
  const roles = usage.roles;
  const outcomeReview = usage.outcomeReview;
  const taskSelection = taskLoopCoverage().selection ?? {};
  const modelUsage = list(usage.modelUsage);
  const hasCoverage = census || Object.keys(taskSelection).length || roles || accounting;
  if (!hasCoverage && !modelUsage.length && !usage.reviewLead) return null;

  return (
    <Stack gap={12}>
      {census || Object.keys(taskSelection).length ? (
        <Text size="sm" tone="secondary" style={taskLoopReaderCopyStyle}>
          {taskLoopMeasurementBoundaryText(taskSelection, census)}
        </Text>
      ) : null}
      {roles || accounting ? (
        <Grid columns="repeat(auto-fit, minmax(min(100%, 240px), 1fr))" gap={20} align="start">
          {roles ? (
            <Stack gap={7}>
              <Text size="sm" weight="semibold">{taskLoopCopy("Session composition", "会话构成")}</Text>
              <UsageStatRow label={taskLoopCopy("User-thread candidates", "用户任务候选")} value={formatUsageNumber(roles.userThreadCandidateCount)} />
              <UsageStatRow label={taskLoopCopy("Child-agent candidates", "子 Agent 候选")} value={formatUsageNumber(roles.childAgentCandidateCount)} />
            </Stack>
          ) : null}
          {accounting ? (
            <Stack gap={7}>
              <Text size="sm" weight="semibold">{taskLoopCopy("Measurement coverage", "计量覆盖")}</Text>
              <UsageStatRow label={taskLoopCopy("Responses", "响应数")} value={formatUsageNumber(accounting.responseCount)} />
              <UsageStatRow label={taskLoopCopy("Model-attributed responses", "已归属模型响应")} value={formatUsageNumber(accounting.modelAttributedResponseCount)} />
              <UsageStatRow label={taskLoopCopy("Usage fields observed", "观察到用量字段")} value={formatUsageNumber(accounting.usageFieldObservedCount)} />
              <UsageStatRow label={taskLoopCopy("Non-zero usage records", "非零用量记录")} value={formatUsageNumber(accounting.nonZeroUsageCount)} />
              <UsageStatRow label={taskLoopCopy("Exact credits available", "可精确计算 credit")} value={accounting.exactCreditsAvailable ? taskLoopCopy("Yes", "是") : taskLoopCopy("No", "否")} />
            </Stack>
          ) : null}
        </Grid>
      ) : null}
      {modelUsage.length ? <TaskLoopModelUsageTable rows={modelUsage} /> : null}
      <Text size="sm" tone="tertiary" style={taskLoopReaderCopyStyle}>
        {accounting?.mode === "effort-proxy"
          ? taskLoopCopy("Active time and model-session counts are effort proxies; exact token or credit savings are unavailable.", "活跃时间和模型会话数仅代表投入；目前无法精确计算 token 或 credit 节省。")
          : taskLoopCopy("Usage totals describe observed activity, not counterfactual savings.", "用量只描述已观察活动，不代表反事实节省。")}
        {outcomeReview && !outcomeReview.comparableModelOutcomeEvidence
          ? taskLoopCopy(" Model outcomes need a controlled A/B before comparison.", " 模型效果需要通过受控 A/B 后才能比较。")
          : ""}
      </Text>
    </Stack>
  );
}

function usageTrendLeader(series) {
  return series.reduce((leader, row) => Number(row.total ?? 0) > Number(leader?.total ?? -1) ? row : leader, null);
}

function usageTrendRange(categories) {
  if (!categories.length) return taskLoopCopy("Latest observations", "最近观测");
  if (categories.length === 1) return categories[0];
  return `${categories[0]} – ${categories[categories.length - 1]}`;
}

function TaskLoopUsageTrend({ title, totalLabel, leaderDescription, series, categories }) {
  if (!series.length) return null;
  const total = series.reduce((sum, row) => sum + Number(row.total ?? 0), 0);
  const leader = usageTrendLeader(series);
  const range = usageTrendRange(categories);
  return (
    <ChartContainer
      title={title}
      description={taskLoopCopy(
        `${range} · ${formatUsageNumber(total)} ${totalLabel}`,
        `${range} · 共 ${formatUsageNumber(total)} ${totalLabel}`,
      )}
      footer={leader ? `${usageSeriesLabel(leader.name)} · ${formatUsageNumber(leader.total)} ${totalLabel}` : undefined}
      caption={leader ? leaderDescription : undefined}
    >
      <AreaChart categories={categories} series={series} height={190} showYAxis legendPosition="bottom" />
    </ChartContainer>
  );
}

function TaskLoopUsageTrends({ activity }) {
  if (!activity) return null;
  const chartWindow = usageChartWindow(activity);
  const categories = chartWindow.categories;
  const modelSeries = visibleUsageSeries(activity.models, chartWindow.offset);
  const skillSeries = visibleUsageSeries(activity.skills, chartWindow.offset);
  if (!modelSeries.length && !skillSeries.length) return null;
  return (
    <ChartComparisonGrid minColumnWidth={360} gap={12} plotHeight={190}>
      <TaskLoopUsageTrend
        title={taskLoopCopy("Model session trend", "模型会话趋势")}
        totalLabel={taskLoopCopy("model-session observations", "个模型会话")}
        leaderDescription={taskLoopCopy("Most observed model in this window", "当前时间窗口内记录最多的模型")}
        series={modelSeries}
        categories={categories}
      />
      <TaskLoopUsageTrend
        title={taskLoopCopy("Skill use trend", "Skill 使用趋势")}
        totalLabel={taskLoopCopy("observations", "次记录")}
        leaderDescription={taskLoopCopy("Most observed Skill in this window", "当前时间窗口内记录次数最多的 Skill")}
        series={skillSeries}
        categories={categories}
      />
    </ChartComparisonGrid>
  );
}

function taskLoopSessionInsightTitle(id) {
  const labels = {
    "session-insight:source-coverage": ["Source coverage", "数据覆盖"],
    "session-insight:validation-behavior": ["Validation behavior", "验证行为"],
    "session-insight:post-edit-validation": ["Post-edit validation", "改动后验证"],
    "session-insight:execution-friction": ["Execution friction", "执行摩擦"],
    "session-insight:tool-mix": ["Tool mix", "工具使用"],
    "session-insight:observed-hooks": ["Observed hooks", "Hook 执行"],
    "session-insight:planning-workflow": ["Planning workflow", "规划工作流"],
    "session-insight:session-complexity": ["Session complexity", "会话复杂度"],
    "session-insight:session-usage-efficiency": ["Session effort", "会话投入"],
  }[id];
  return labels ? taskLoopCopy(labels[0], labels[1]) : id;
}

function taskLoopSessionInsightConfidence(row) {
  return list(row?.labels).map(textValue).find((value) => ["High", "Medium", "Low"].includes(value)) ?? "";
}

function taskLoopSessionInsightColumns() {
  return [
    {
      key: "id",
      title: taskLoopCopy("Observation", "观察主题"),
      minWidth: "150px",
      render: (row) => <Text size="sm" weight="semibold">{taskLoopSessionInsightTitle(row.id)}</Text>,
    },
    {
      key: "summary",
      title: taskLoopCopy("What was observed", "观察说明"),
      minWidth: "420px",
      render: (row) => <Text size="sm" tone="secondary">{row.summary ?? "—"}</Text>,
    },
    {
      key: "confidence",
      title: taskLoopCopy("Confidence", "可信度"),
      minWidth: "120px",
      render: (row) => {
        const confidence = taskLoopSessionInsightConfidence(row);
        return confidence ? <Tag size="sm" tone={confidenceTone(confidence)}>{confidenceLabel(confidence)}</Tag> : <Text size="sm" tone="tertiary">—</Text>;
      },
    },
    {
      key: "evidenceRefs",
      title: taskLoopCopy("Evidence", "证据"),
      width: "80px",
      align: "right",
      render: (row) => <Text size="sm" weight="semibold">{list(row.evidenceRefs).length}</Text>,
    },
  ];
}

function taskLoopSessionInsightDetail(row) {
  const evidenceRefs = list(row?.evidenceRefs);
  return (
    <CollapsibleSection
      size="sm"
      defaultOpen={false}
      title={<Text size="sm" weight="semibold">{taskLoopCopy("View raw observation metadata", "查看原始观察元数据")}</Text>}
      bodyStyle={{ padding: "6px 0 2px 16px" }}
      headerStyle={{ borderBottom: "none", minHeight: 24 }}
    >
      <Stack gap={7}>
        <Text size="sm" tone="tertiary">
          {taskLoopCopy("Insight ID", "洞察 ID")}: {row.id} · {taskLoopCopy("Status", "状态")}: {row.status ?? "—"} · {taskLoopCopy("Kind", "类型")}: {row.kind ?? "—"} · {taskLoopCopy("Model", "模型版本")}: {row.modelVersion ?? "—"}
        </Text>
        {list(row.labels).length ? (
          <Row gap={5} wrap>{row.labels.map((label) => <Tag key={label} size="sm" tone="neutral">{label}</Tag>)}</Row>
        ) : null}
        {evidenceRefs.length ? <EvidenceReferenceList items={evidenceRefs} /> : <Text size="sm" tone="tertiary">{taskLoopCopy("No raw evidence references recorded.", "未记录原始证据引用。")}</Text>}
      </Stack>
    </CollapsibleSection>
  );
}

function taskLoopRepresentativeSessionInsights(entries) {
  const preferredIds = [
    "session-insight:post-edit-validation",
    "session-insight:execution-friction",
    "session-insight:tool-mix",
  ];
  const preferred = preferredIds
    .map((id) => entries.find((entry) => entry?.id === id))
    .filter((entry) => entry && list(entry.evidenceRefs).length > 0);
  const remaining = entries
    .filter((entry) => list(entry?.evidenceRefs).length > 0 && !preferred.includes(entry))
    .sort((left, right) => list(right.evidenceRefs).length - list(left.evidenceRefs).length);
  return [...preferred, ...remaining].slice(0, 3);
}

function TaskLoopSessionInsightsDialog({ entries }) {
  return (
    <Dialog
      trigger={(
        <Button variant="ghost">
          {taskLoopCopy(`View all ${entries.length}`, `查看全部 ${entries.length} 条`)}
        </Button>
      )}
      title={taskLoopCopy("All session observations", "全部会话观察")}
      closeLabel={taskLoopCopy("Close", "关闭")}
      maxWidth={1040}
    >
      <Stack gap={10}>
        <Text size="sm" tone="secondary" style={taskLoopReaderCopyStyle}>
          {taskLoopCopy(
            "These are candidate observations projected from session evidence. Read them as investigation leads, not confirmed user intent or outcome claims.",
            "这些是从会话证据投影出的候选观察，用于指引后续调查，不等同于已确认的用户意图或结果结论。",
          )}
        </Text>
        <Table
          columns={taskLoopSessionInsightColumns()}
          rows={entries}
          rowKey={(row) => row.id}
          density="compact"
          renderDetail={taskLoopSessionInsightDetail}
        />
      </Stack>
    </Dialog>
  );
}

function TaskLoopSessionInsights() {
  const entries = list(report.summary?.semanticFacets?.entries);
  if (!entries.length) return null;
  const representativeEntries = taskLoopRepresentativeSessionInsights(entries);
  const rowTones = ["success", "warning", "info"];
  return (
    <Stack gap={10}>
      <Row gap={8} align="center" wrap>
        <H2 style={{ fontSize: 18, lineHeight: 1.35 }}>{taskLoopCopy("Session observations", "会话观察")}</H2>
        <Tag size="sm" tone="neutral">{entries.length} {taskLoopCopy("observations", "条观察")}</Tag>
      </Row>
      <Text size="sm" tone="secondary" style={taskLoopReaderCopyStyle}>
        {taskLoopCopy(
          "Representative evidence-bearing observations for follow-up investigation and priority judgment.",
          "按主题呈现的代表性观察，用于指引后续调查与优先级判断。",
        )}
      </Text>
      <Grid columns={3} minColumnWidth={220} gap={8} align="stretch">
        {representativeEntries.map((row, index) => {
          const confidence = taskLoopSessionInsightConfidence(row);
          const evidenceCount = list(row.evidenceRefs).length;
          return (
            <Card key={row.id} size="sm">
              <CardBody style={{ padding: 12, height: 142 }}>
                <Stack gap={8} style={{ height: "100%" }}>
                  <Row gap={7} align="center" style={{ minWidth: 0 }}>
                    <Tag size="sm" tone={rowTones[index]}>{index + 1}</Tag>
                    <Text size="sm" weight="semibold" truncate>{taskLoopSessionInsightTitle(row.id)}</Text>
                  </Row>
                  <Text
                    size="sm"
                    tone="secondary"
                    style={{
                      display: "-webkit-box",
                      WebkitLineClamp: 2,
                      WebkitBoxOrient: "vertical",
                      overflow: "hidden",
                      lineHeight: 1.5,
                    }}
                  >
                    {row.summary ?? "—"}
                  </Text>
                  <Row justify="space-between" align="center" wrap gap={6} style={{ marginTop: "auto" }}>
                    {confidence ? <Tag size="sm" tone={confidenceTone(confidence)}>{confidenceLabel(confidence)}</Tag> : null}
                    <Text size="sm" tone="tertiary">
                      {evidenceCount} {taskLoopCopy(evidenceCount === 1 ? "evidence item" : "evidence items", "条证据")}
                    </Text>
                  </Row>
                </Stack>
              </CardBody>
            </Card>
          );
        })}
      </Grid>
      <Row justify="flex-end">
        <TaskLoopSessionInsightsDialog entries={entries} />
      </Row>
    </Stack>
  );
}

function TaskLoopFindingItem({ row }) {
  const expectedOutput = list(row.expectedOutput).filter((item) => textValue(item));
  const [dimensionRef] = list(row.dimensionRefs);
  return (
    <ImprovementDisclosure
      severity={severityLabel(row.severity)}
      severityTone={severityTone(row.severity)}
      stage={dimensionRef ? taskLoopDimensionLabel(dimensionRef) : undefined}
      title={row.title ?? row.id}
      description={textValue(row.reason) || undefined}
      cause={textValue(row.reason) || taskLoopCopy("No cause was recorded.", "未记录原因。")}
      expectedOutputs={expectedOutput}
      action={(
        <SendToChatButton
          variant="tertiary"
          text={row.aiFixPrompt}
          options={{ submit: false }}
          disabled={!row.aiFixPrompt}
          style={{ fontSize: 11, lineHeight: "14px" }}
        >
          {taskLoopCopy("Plan AI Fix", "规划 AI 修复")}
        </SendToChatButton>
      )}
      labels={{
        details: taskLoopCopy("View details", "查看详情"),
        cause: taskLoopCopy("Cause", "原因"),
        expectedOutput: taskLoopCopy("Expected Output", "预期结果"),
        note: taskLoopCopy(
          "Plan AI Fix opens the full fix instructions in chat for review before sending.",
          "规划 AI 修复会先在对话中打开完整修复指令，供发送前检查。",
        ),
      }}
    />
  );
}

function TaskLoopFindingList({ findings }) {
  return (
    <>
      {findings.map((row) => <TaskLoopFindingItem key={row.id} row={row} />)}
    </>
  );
}

function taskLoopSuggestionKindLabel(kind) {
  const labels = {
    "try-existing": ["Try existing", "试用已有能力"],
    "working-pattern": ["Working pattern", "有效模式"],
    "loop-candidate": ["Loop candidate", "循环候选"],
    horizon: ["Horizon", "中长期"],
  }[kind] ?? ["Suggestion", "建议"];
  return taskLoopCopy(labels[0], labels[1]);
}

function TaskLoopSuggestionDialog({ row }) {
  const prerequisites = list(row.prerequisites).map(textValue).filter(Boolean);
  const blockedBy = list(row.blockedBy).map(textValue).filter(Boolean);
  return (
    <Dialog
      trigger={<Button variant="ghost">{taskLoopCopy("Review suggestion", "查看建议")}</Button>}
      title={row.title ?? row.id}
      closeLabel={taskLoopCopy("Close", "关闭")}
      maxWidth={760}
    >
      <Stack gap={16}>
        <Row gap={6} align="center" wrap>
          <Tag size="sm" tone="info">{taskLoopSuggestionKindLabel(row.kind)}</Tag>
          <Tag size="sm" tone={confidenceTone(row.confidence)}>{confidenceLabel(row.confidence)}</Tag>
        </Row>
        <Stack gap={6} style={taskLoopReaderCopyStyle}>
          <Text size="sm" weight="semibold" tone="tertiary">{taskLoopCopy("Why this is worth trying", "为什么值得尝试")}</Text>
          <Text size="sm" tone="secondary" style={{ lineHeight: 1.6 }}>{row.reason}</Text>
        </Stack>
        <Divider style={{ margin: "2px 0" }} />
        <Grid columns={2} minColumnWidth={240} gap={12}>
          <Stack gap={5}>
            <Text size="sm" weight="semibold" tone="tertiary">{taskLoopCopy("Owner", "负责人")}</Text>
            <Text size="sm" tone="secondary">{row.owner}</Text>
          </Stack>
          <Stack gap={5}>
            <Text size="sm" weight="semibold" tone="tertiary">{taskLoopCopy("Validation", "验证")}</Text>
            <Text size="sm" tone="secondary">{row.validation}</Text>
          </Stack>
        </Grid>
        <Stack gap={5} style={taskLoopReaderCopyStyle}>
          <Text size="sm" weight="semibold" tone="tertiary">{taskLoopCopy("Next step", "下一步")}</Text>
          <Text size="sm" tone="secondary" style={{ lineHeight: 1.6 }}>{row.nextStep}</Text>
        </Stack>
        {prerequisites.length ? (
          <Stack gap={6}>
            <Text size="sm" weight="semibold" tone="tertiary">{taskLoopCopy("Prerequisites", "前置条件")}</Text>
            {prerequisites.map((item, index) => (
              <Row key={`${row.id}-prerequisite-${index}`} gap={8} align="start">
                <Tag size="sm" tone="neutral">{index + 1}</Tag>
                <Text size="sm" tone="secondary">{item}</Text>
              </Row>
            ))}
          </Stack>
        ) : null}
        {blockedBy.length ? (
          <Stack gap={6}>
            <Text size="sm" weight="semibold" tone="tertiary">{taskLoopCopy("Blocked by", "阻塞项")}</Text>
            {blockedBy.map((item, index) => (
              <Row key={`${row.id}-blocker-${index}`} gap={8} align="start">
                <Tag size="sm" tone="warning">{index + 1}</Tag>
                <Text size="sm" tone="secondary">{item}</Text>
              </Row>
            ))}
          </Stack>
        ) : null}
      </Stack>
    </Dialog>
  );
}

function TaskLoopSuggestionCard({ row }) {
  return (
    <Card size="sm">
      <CardBody style={{ padding: 14, minHeight: 260, height: "100%" }}>
        <Stack gap={9} style={{ height: "100%" }}>
          <Row gap={6} align="center" wrap>
            <Tag size="sm" tone="info">{taskLoopSuggestionKindLabel(row.kind)}</Tag>
            <Tag size="sm" tone={confidenceTone(row.confidence)}>{confidenceLabel(row.confidence)}</Tag>
          </Row>
          <Text
            weight="semibold"
            style={{ fontSize: 15, lineHeight: "21px", display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}
          >
            {row.title ?? row.id}
          </Text>
          <Text
            size="sm"
            tone="secondary"
            style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden", lineHeight: 1.5 }}
          >
            {row.reason}
          </Text>
          <Stack gap={3} style={{ marginTop: "auto" }}>
            <Text size="sm" weight="semibold" tone="tertiary">{taskLoopCopy("Next step", "下一步")}</Text>
            <Text size="sm" tone="secondary" truncate>{row.nextStep}</Text>
          </Stack>
          <Divider style={{ margin: "2px 0" }} />
          <Row justify="space-between" align="center" wrap gap={8}>
            <Text size="sm" tone="tertiary" truncate>{row.owner}</Text>
            <TaskLoopSuggestionDialog row={row} />
          </Row>
        </Stack>
      </CardBody>
    </Card>
  );
}

function TaskLoopSuggestionCards({ suggestions }) {
  return (
    <Grid columns={3} minColumnWidth={280} gap={8} align="stretch">
      {suggestions.map((row) => <TaskLoopSuggestionCard key={row.id} row={row} />)}
    </Grid>
  );
}

function taskLoopDeliveryOutcomeLabel(boundary) {
  if (!boundary || !Object.hasOwn(boundary, "deliveryEvidenceLevels")) {
    return taskLoopCopy("Not supplied", "未提供");
  }
  const levels = list(boundary?.deliveryEvidenceLevels).map(textValue).filter(Boolean);
  if (!levels.length || levels.every((value) => ["none", "unobserved", "not-observed"].includes(value.toLowerCase()))) {
    return taskLoopCopy("Not observed", "未观察到");
  }
  return levels.join(", ");
}

function taskLoopCoverageFraction(value, analyzedField, eligibleField) {
  if (!value || !Object.hasOwn(value, analyzedField) || !Object.hasOwn(value, eligibleField)) return null;
  const analyzed = Number(value[analyzedField]);
  const eligible = Number(value[eligibleField]);
  return Number.isInteger(analyzed) && analyzed >= 0 && Number.isInteger(eligible) && eligible >= 0
    ? `${formatUsageNumber(analyzed)}/${formatUsageNumber(eligible)}`
    : null;
}

function taskLoopMeasurementBoundaryText(selection, usageSelection) {
  const sample = taskLoopCoverageFraction(selection, "analyzedCount", "eligibleCount");
  const activity = taskLoopCoverageFraction(usageSelection, "analyzedSessionCount", "eligibleSessionCount");
  if (sample && activity) {
    return taskLoopCopy(
      `Work-stage conclusions use ${sample} stratified sample sessions; activity accounting covers ${activity}. Use this evidence to locate review leads, not to prove efficiency or model quality.`,
      `工作环节结论来自 ${sample} 个分层抽样会话；活动统计覆盖 ${activity}。当前证据可用于定位线索，不足以证明效率或模型质量。`,
    );
  }
  if (sample) {
    return taskLoopCopy(
      `Work-stage conclusions use ${sample} stratified sample sessions. Activity and model accounting were not supplied, so no usage conclusion is available.`,
      `工作环节结论来自 ${sample} 个分层抽样会话。未提供活动与模型统计，因此无法形成用量结论。`,
    );
  }
  if (activity) {
    return taskLoopCopy(
      `Activity accounting covers ${activity}. Sampling provenance was not supplied, so usage is shown without a work-stage sampling conclusion.`,
      `活动统计覆盖 ${activity}。未提供抽样来源，因此这里只展示用量，不形成工作环节抽样结论。`,
    );
  }
  return taskLoopCopy(
    "Session measurement context was not supplied, so no sampling, usage, or model conclusion is available.",
    "未提供会话计量上下文，因此无法形成抽样、用量或模型结论。",
  );
}

function taskLoopMeasurementBoundaryTone(selection, usageSelection) {
  const sample = taskLoopCoverageFraction(selection, "analyzedCount", "eligibleCount");
  const activity = taskLoopCoverageFraction(usageSelection, "analyzedSessionCount", "eligibleSessionCount");
  return sample && activity ? "info" : "warning";
}

function taskLoopMeasurementModelLabel(usage, modelUsage) {
  if (!usage) return taskLoopCopy("Usage unavailable", "用量不可用");
  return `${modelUsage.length} ${taskLoopCopy("models", "个模型")}`;
}

function taskLoopSelectionDetailText(selection) {
  const coverage = taskLoopCoverageFraction(selection, "analyzedCount", "eligibleCount");
  if (!coverage) return taskLoopCopy("Not supplied", "未提供");
  return taskLoopCopy(
    `${textValue(selection.strategy) || "unknown"}; ${coverage} eligible sessions analyzed.`,
    `${textValue(selection.strategy) || "未知"}；已分析 ${coverage} 个符合条件的会话。`,
  );
}

function taskLoopTaskEvidenceText(coverage) {
  const fields = ["episodeCount", "editedEpisodeCount", "closedEpisodeCount", "recoveredEpisodeCount"];
  if (!fields.every((field) => Object.hasOwn(coverage, field))) return taskLoopCopy("Not supplied", "未提供");
  const [episodes, edited, closed, recovered] = fields.map((field) => Number(coverage[field]));
  if (![episodes, edited, closed, recovered].every((value) => Number.isInteger(value) && value >= 0)) {
    return taskLoopCopy("Not supplied", "未提供");
  }
  return taskLoopCopy(
    `${episodes} episodes; ${edited} with changes; ${closed} closed; ${recovered} repaired and passed.`,
    `${episodes} 个任务片段；${edited} 个包含改动；${closed} 个已闭环；${recovered} 个修复并通过。`,
  );
}

function taskLoopLearningDetailText(learning) {
  if (!Object.hasOwn(learning, "state") && !Array.isArray(learning.interventions)) {
    return taskLoopCopy("Not supplied", "未提供");
  }
  return taskLoopCopy(
    `${learningStateLabel(learning.state)}; ${list(learning.interventions).length} declared intervention(s).`,
    `${learningStateLabel(learning.state)}；${list(learning.interventions).length} 项已声明的改进。`,
  );
}

function TaskLoopEvidenceFact({ label, value, tone = "neutral" }) {
  return (
    <Card size="sm">
      <CardBody style={{ padding: "10px 12px" }}>
        <Stack gap={6}>
          <Text size="sm" tone="tertiary">{label}</Text>
          <Row>
            <Tag size="sm" tone={tone}>{value}</Tag>
          </Row>
        </Stack>
      </CardBody>
    </Card>
  );
}

function TaskLoopEvidenceDetails({ usage, boundary, manifest, selection, learning, coverage }) {
  const hasDetailedBoundary = Object.keys(boundary).length > 0
    || Object.keys(coverage).length > 0
    || Object.keys(learning).length > 0;
  return (
    <Stack gap={14}>
      {hasDetailedBoundary ? (
        <Stack gap={7} style={taskLoopReaderCopyStyle}>
          <Text weight="semibold">{taskLoopCopy("Sampling and provenance", "抽样与来源")}</Text>
          <Text size="sm" tone="secondary">{taskLoopCopy("Session selection", "会话抽样")}: {taskLoopSelectionDetailText(selection)}</Text>
          <Text size="sm" tone="secondary">{taskLoopCopy("Task evidence", "任务证据")}: {taskLoopTaskEvidenceText(coverage)}</Text>
          <Text size="sm" tone="secondary">{taskLoopCopy("Delivery outcomes", "交付结果")}: {taskLoopDeliveryOutcomeLabel(boundary)}.</Text>
          <Text size="sm" tone="secondary">{taskLoopCopy("Learning comparison", "学习循环比较")}: {taskLoopLearningDetailText(learning)}</Text>
          {textValue(learning.summary) ? <Text size="sm" tone="secondary">{learning.summary}</Text> : null}
        </Stack>
      ) : (
        <Text size="sm" tone="secondary" style={taskLoopReaderCopyStyle}>
          {taskLoopCopy(
            "This run did not supply task-episode or delivery-outcome evidence, so the report only shows repository signals it could verify.",
            "本次运行没有提供任务片段或交付结果证据，因此报告只展示能够验证的仓库信号。",
          )}
        </Text>
      )}
      <Grid columns={3} minColumnWidth={260} gap={12} align="start">
        <UsageStatRow label={taskLoopCopy("Evidence mode", "证据模式")} value={report.summary?.evidenceMode ?? "—"} />
        <UsageStatRow label={taskLoopCopy("Platform", "平台")} value={manifest.platform ?? "—"} />
        <UsageStatRow label={taskLoopCopy("Adapter version", "适配器版本")} value={manifest.adapterVersion ?? "—"} />
        <UsageStatRow label={taskLoopCopy("Manifest schema", "清单版本")} value={manifest.schemaVersion ?? "—"} />
        <UsageStatRow label={taskLoopCopy("Source fingerprint", "来源指纹")} value={manifest.sourceFingerprint ?? "—"} />
        <UsageStatRow label={taskLoopCopy("Source gaps", "来源缺口")} value={Array.isArray(boundary.sourceGaps) ? boundary.sourceGaps.length : "—"} />
      </Grid>
      {usage ? (
        <>
          <Divider style={{ margin: "2px 0" }} />
          <TaskLoopUsageMethodology usage={usage} />
        </>
      ) : null}
    </Stack>
  );
}

function TaskLoopEvidenceBoundary({ usage }) {
  const boundary = report.summary?.evidenceBoundary ?? {};
  const manifest = boundary.manifest ?? {};
  const selection = manifest.selection ?? {};
  const learning = report.summary?.learningCapture ?? {};
  const coverage = taskLoopCoverage();
  const modelUsage = list(usage?.modelUsage);
  const longSessionSamples = list(usage?.longSessions?.samples);
  const sessionInsights = list(report.summary?.semanticFacets?.entries);
  const activitySelection = usage?.selection ?? {};
  const samplingConfidence = textValue(selection.confidence)
    || taskLoopCopy("Not supplied", "未提供");
  const sourceGapValue = Array.isArray(boundary.sourceGaps) ? boundary.sourceGaps.length : "—";

  return (
    <Stack gap={12}>
      <Callout tone={taskLoopMeasurementBoundaryTone(selection, activitySelection)}>
        <Text size="sm" style={{ lineHeight: 1.5 }}>
          {taskLoopMeasurementBoundaryText(selection, activitySelection)}
        </Text>
      </Callout>
      <Grid columns={3} minColumnWidth={180} gap={8} align="stretch">
        <TaskLoopEvidenceFact label={taskLoopCopy("Sampling confidence", "抽样可信度")} value={samplingConfidence} tone={confidenceTone(selection.confidence)} />
        <TaskLoopEvidenceFact label={taskLoopCopy("Source gaps", "来源缺口")} value={sourceGapValue} tone={Array.isArray(boundary.sourceGaps) && boundary.sourceGaps.length ? "warning" : "neutral"} />
        <TaskLoopEvidenceFact label={taskLoopCopy("Delivery outcome", "交付结果")} value={taskLoopDeliveryOutcomeLabel(boundary)} />
      </Grid>
      {usage?.reviewLead && longSessionSamples.length ? (
        <>
          <Divider style={{ margin: "2px 0" }} />
          <TaskLoopLongSessionReview usage={usage} />
        </>
      ) : null}
      {sessionInsights.length ? (
        <>
          <Divider style={{ margin: "2px 0" }} />
          <TaskLoopSessionInsights />
        </>
      ) : null}
      <Divider style={{ margin: "2px 0" }} />
      <CollapsibleSection
        size="sm"
        defaultOpen={false}
        title={<Text weight="semibold">{taskLoopCopy("View measurement and model details", "查看计量与模型明细")}</Text>}
        trailing={<Text size="sm" tone="secondary">{taskLoopMeasurementModelLabel(usage, modelUsage)}</Text>}
        bodyStyle={{ padding: "12px 0 2px 16px" }}
        headerStyle={{ minHeight: 44 }}
      >
        <TaskLoopEvidenceDetails
          usage={usage}
          boundary={boundary}
          manifest={manifest}
          selection={selection}
          learning={learning}
          coverage={coverage}
        />
      </CollapsibleSection>
    </Stack>
  );
}

function TaskLoopReport() {
  const dimensions = list(report.summary?.dimensions);
  const findings = list(report.findings).sort((left, right) => severityRank(left.severity) - severityRank(right.severity));
  const suggestions = list(report.summary?.suggestions);
  const activity = taskLoopUsageActivity();
  const usage = taskLoopUsageEfficiency();
  const highFindings = findings.filter((row) => row.severity === "Critical" || row.severity === "High");
  const mediumFindings = findings.filter((row) => row.severity === "Medium");
  return (
    <ReportShell ariaLabel={taskLoopCopy("Better Harness report", "Better Harness 报告")} density="comfortable">
      <Stack gap={canvasSemanticSpacing.reportSection}>
        <TaskLoopReportHeader findings={findings} />

        <ReportSection
          title={taskLoopCopy("Agent Work Loop", "Agent 工作流")}
          meta={`${dimensions.length} ${taskLoopCopy("dimensions", "个维度")}`}
          compact
        >
          <TaskLoopFluency dimensions={dimensions} />
        </ReportSection>

        {activity || usage ? (
          <ReportSection title={taskLoopCopy("Project usage", "项目用量")}>
          <TaskLoopProjectUsage activity={activity} usage={usage} />
          </ReportSection>
        ) : null}

        <ImprovementList
          id="prioritized-improvements"
          title={taskLoopCopy("Prioritized improvements", "优先优化项")}
          summary={taskLoopCopy(
            `${findings.length} total · ${highFindings.length} high · ${mediumFindings.length} medium`,
            `共 ${findings.length} 项 · ${highFindings.length} 个高优先级 · ${mediumFindings.length} 个中优先级`,
          )}
        >
        <TaskLoopFindingList findings={findings} />
        {suggestions.length ? (
          <>
            <Divider style={{ margin: "8px 0 2px" }} />
            <Row justify="space-between" align="center" wrap gap={8}>
              <Stack gap={3}>
                <Text weight="semibold">{taskLoopCopy("Suggestions", "建议")}</Text>
                <Text size="sm" tone="secondary" style={taskLoopReaderCopyStyle}>
                  {taskLoopCopy(
                    "Evidence-bound capabilities and patterns worth trying next. Suggestions are advisory and do not include an AI Fix action.",
                    "基于证据、值得下一步尝试的能力与模式。建议仅供参考，不包含 AI 修复动作。",
                  )}
                </Text>
              </Stack>
              <Tag size="sm" tone="neutral">{suggestions.length} {taskLoopCopy("suggestions", "条建议")}</Tag>
            </Row>
            <TaskLoopSuggestionCards suggestions={suggestions} />
          </>
        ) : null}
        </ImprovementList>

        <ReportSection
          title={taskLoopCopy("Agent Customize", "Agent 自定义")}
          description={taskLoopCopy(
            "Discovered sources, not a quality or maturity score.",
            "这里只展示已发现来源，不代表质量或成熟度评分。",
          )}
        >
        <TaskLoopPracticeTable rows={practiceRows()} />
        {repositorySourceRows().length ? (
          <Stack gap={16} style={{ marginTop: 16 }}>
            <Divider />
            <Stack gap={4}>
              <Text weight="semibold">{taskLoopCopy("Repository provider sources", "仓库中的 provider 源码资产")}</Text>
              <Text size="sm" tone="secondary" style={taskLoopReaderCopyStyle}>
                {taskLoopCopy(
                  "These rows explain provider-owned assets not reconciled into the active table. They are inventory leads; active state comes from the selected provider, and the rows are not quality findings.",
                  "这些行说明尚未归入活跃表的 provider 资产；它们只是清单线索，活跃状态以当前 provider 为准，也不是质量问题。",
                )}
              </Text>
            </Stack>
            <Grid columns={2} minColumnWidth={300} gap={12} align="stretch">
              {repositorySourceRows().map((row, index) => (
                <RepositorySourceCard key={`${row.provider}-${row.owner}-${row.surface}-${index}`} row={row} />
              ))}
            </Grid>
          </Stack>
        ) : null}
        </ReportSection>

        {activity ? (
          <ReportSection title={taskLoopCopy("Usage trends", "用量趋势")}>
            <TaskLoopUsageTrends activity={activity} />
          </ReportSection>
        ) : null}

        <ReportSection title={taskLoopCopy("Evidence and methodology", "证据与方法")}>
          <TaskLoopEvidenceBoundary usage={usage} />
        </ReportSection>
      </Stack>
    </ReportShell>
  );
}

export default function QoderHarnessReport() {
  return <TaskLoopReport />;
}
