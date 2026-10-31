import { TAB_BAR_BOTTOM_SPACE } from './layout';
import React, { useContext, useState } from 'react';
import { Image, Pressable, ScrollView, TextInput, View } from 'react-native';
import { LinearGradient } from 'expo-linear-gradient';
import type { AppState, Lesson, Simulation } from '../shared/types';
import { BusyContext, Button, Chip, colors, Icon, Mascot, ProgressBar, s, Txt, type IconName } from './ui';
import { practiceGuides, practiceTerms } from './practice-content';

type Mode = 'route' | 'simulators' | 'guides';
type Filter = 'all' | 'available' | 'completed';
type Props = {
  state: AppState; motion: boolean; onInvestments: () => void;
  onLesson: (id: string) => void; onSimulation: (id: string) => void;
  onAssessment: () => void; onGoal: () => void; onLab?: () => void;
};
const chapters = [
  { title: 'Собираем план', description: 'От свободных денег — к посильному взносу', icon: 'flag', color: '#DEEAFE', ink: '#315798' },
  { title: 'Меняем условия', description: 'Когда жизнь не совпадает с расчётами', icon: 'refresh', color: '#E8DFFF', ink: '#6F519A' },
] as const;

function TinyAction({ label, onPress, icon = 'arrow', color = colors.deepBlue }: {
  label: string; onPress: () => void; icon?: IconName; color?: string;
}) {
  const busy = useContext(BusyContext);
  return <Pressable accessibilityRole="button" accessibilityLabel={label} disabled={busy} aria-disabled={busy}
    onPress={onPress} style={{ flexDirection: 'row', alignItems: 'center', gap: 8, minHeight: 44 }}>
    <Txt size={13} weight="bold" color={color} style={{ flexShrink: 1 }}>{label}</Txt><Icon name={icon} size={17} color={color}/>
  </Pressable>;
}

function AssessmentPanel({ state, onAssessment, onGoal }: Pick<Props, 'state' | 'onAssessment' | 'onGoal'>) {
  const a = state.assessment;
  const busy = useContext(BusyContext);
  const [expanded, setExpanded] = useState(false);
  const date = (value: string) => new Date(value).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long' });
  const copy = a.status === 'not_started'
    ? 'После первого ответа начнётся отсчёт. Затем появится другой пример: попробуешь применить подход самостоятельно.'
    : a.status === 'waiting'
      ? `Откроется ${date(a.availableAt!)}. Ответить можно до ${date(a.closesAt!)}. Пока можно вернуться к историям или заняться своей целью.`
      : a.status === 'available'
        ? `Новый пример уже ждёт. Ответь до ${date(a.closesAt!)} — здесь сохраняется одна самостоятельная попытка.`
        : a.status === 'completed'
          ? 'Ты уже ответил на новую ситуацию. Ответ и объяснение сохранены; результат доступен в профиле.'
          : 'Окно этой проверки закончилось без ответа. Это не оценка твоих знаний. Учебные истории остаются доступны.';
  const status = a.status === 'available' ? 'Можно пройти' : a.status === 'waiting' ? `Откроется ${date(a.availableAt!)}` : a.status === 'completed' ? 'Ответ сохранён' : a.status === 'not_started' ? 'После первого ответа' : 'Окно завершено';
  return <View style={{ backgroundColor: '#FFF0CA', borderRadius: 23, paddingHorizontal: 18 }}>
    <Pressable accessibilityRole="button" accessibilityLabel={`Проверка через неделю: ${status}`} accessibilityState={{ expanded, disabled: busy }} aria-expanded={expanded} disabled={busy} onPress={() => setExpanded(!expanded)} style={{ minHeight: 76, flexDirection: 'row', alignItems: 'center', gap: 12 }}>
      <Icon name="calendar" size={22} color="#80612E"/><View style={{ flex: 1, gap: 4 }}><Txt size={15} weight="bold">Проверка через неделю</Txt><Txt size={13} color="#755123">{status}</Txt></View><Icon name={expanded ? 'close' : 'plus'} size={18} color="#80612E"/>
    </Pressable>
    {expanded && <View style={{ gap: 13, paddingBottom: 18 }}><Txt size={13} color="#644C27" style={{ lineHeight: 21 }}>{copy}</Txt>
    {a.status === 'available'
      ? <Button label="Пройти новую ситуацию" onPress={onAssessment} style={{ minHeight: 49, paddingVertical: 12 }}/>
      : <TinyAction label="Перейти к своей цели" onPress={onGoal} color="#755123"/>}
    <Txt size={13} color="#80612E">Окно проверки: от 144 до 216 часов после первого ответа.</Txt></View>}
  </View>;
}

export function Practice({ state, motion, onLesson, onSimulation, onAssessment, onGoal, onLab }: Props) {
  const busy = useContext(BusyContext);
  const [mode, setMode] = useState<Mode>('route');
  const chapterDone = chapters.map((_, i) => state.lessons.slice(i * 3, i * 3 + 3).filter(x => x.completed).length + Number(state.simulations[i]?.completed));
  const [expandedChapters, setExpandedChapters] = useState<number[]>([chapterDone[0] === 4 ? 1 : 0]);
  const [chapterFilters, setChapterFilters] = useState<Record<number, Filter>>({});
  const [filtersOpen, setFiltersOpen] = useState<number | null>(null);
  const [expandedLesson, setExpandedLesson] = useState<string | null>(null);
  const [expandedSimulation, setExpandedSimulation] = useState<string | null>(null);
  const [expandedGuide, setExpandedGuide] = useState<string | null>(null);
  const [expandedTerm, setExpandedTerm] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [topic, setTopic] = useState('Все');
  const count = state.progress.completedLessons + state.progress.completedSimulations;
  const nextLesson = state.lessons.find(x => x.id === state.progress.nextLessonId);
  const nextSimulation = state.simulations.find(x => x.id === state.progress.nextSimulationId);
  const assessmentReady = state.assessment.status === 'available';
  const nextTitle = assessmentReady ? 'Новая ситуация через неделю' : nextSimulation?.title || nextLesson?.title;
  const allDone = count === 8;
  const guideFor = (id: string) => practiceGuides.find(g => g.id === id);
  const matches = (item: Lesson | Simulation, filter: Filter) => filter === 'all' || (filter === 'available' ? item.unlocked && !item.completed : item.completed);
  const search = query.trim().toLocaleLowerCase('ru-RU');
  const guides = practiceGuides.filter(g => (topic === 'Все' || g.topic === topic) && `${g.title} ${g.short} ${g.rule} ${g.example} ${g.action}`.toLocaleLowerCase('ru-RU').includes(search));
  const terms = practiceTerms.filter(t => `${t.term} ${t.meaning}`.toLocaleLowerCase('ru-RU').includes(search));

  function openNext() {
    if (assessmentReady) onAssessment();
    else if (nextSimulation) onSimulation(nextSimulation.id);
    else if (nextLesson) onLesson(nextLesson.id);
    else onGoal();
  }
  function setSection(value: Mode) { setMode(value); setExpandedLesson(null); }
  function prerequisite(id: string) {
    const index = state.lessons.findIndex(x => x.id === id);
    if (index === 3) return 'Сначала пройди симуляцию «Собери свой первый план».';
    const previous = state.lessons[index - 1];
    return previous ? `Откроется после истории «${previous.title}».` : 'История станет доступна после предыдущего шага.';
  }
  function lessonRow(item: Lesson, tone: typeof chapters[number]) {
    const expanded = expandedLesson === item.id;
    const guide = guideFor(item.id);
    return <View key={item.id} style={{ borderBottomWidth: 1, borderBottomColor: colors.line }}>
      <Pressable accessibilityRole="button" accessibilityLabel={`Подробнее: ${item.title}`}
        accessibilityState={{ expanded }} aria-expanded={expanded} disabled={busy}
        onPress={() => setExpandedLesson(expanded ? null : item.id)}
        style={{ flexDirection: 'row', gap: 13, alignItems: 'center', paddingVertical: 17 }}>
        <View style={{ width: 46, height: 46, borderRadius: 17, backgroundColor: item.completed ? colors.mint : item.unlocked ? tone.color : '#F0F2F7', alignItems: 'center', justifyContent: 'center' }}>
          <Icon name={item.completed ? 'check' : item.unlocked ? guide?.icon || 'book' : 'lock'} size={22} color={item.completed ? colors.green : item.unlocked ? tone.ink : colors.muted}/>
        </View>
        <View style={{ flex: 1, gap: 5 }}>
          <Txt size={15} weight="bold">{item.title}</Txt>
          <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: 5 }}><Txt size={11} color={colors.muted}>{item.duration}</Txt><Txt size={11} color={item.completed ? colors.green : item.unlocked ? colors.deepBlue : colors.muted}>· {item.completed ? 'Разобрано' : item.unlocked ? 'Можно начать' : 'Пока закрыто'}</Txt></View>
        </View>
        <Icon name={expanded ? 'close' : 'plus'} size={18} color={colors.muted}/>
      </Pressable>
      {expanded && <View style={{ marginLeft: 3, paddingBottom: 18, gap: 13 }}>
        <View style={{ padding: 15, backgroundColor: '#F6F7FB', borderRadius: 18, gap: 7 }}>
          <Txt size={13} weight="bold">Что попробуешь</Txt><Txt size={13} color={colors.muted} style={{ lineHeight: 21 }}>{guide?.takeaway || item.subtitle}</Txt>
        </View>
        <Txt size={13} style={{ lineHeight: 22 }}>{item.context}</Txt>
        {item.unlocked
          ? <Button label={item.completed ? 'Повторить историю' : 'Начать историю'} onPress={() => onLesson(item.id)} style={{ minHeight: 49, paddingVertical: 12 }}/>
          : <View style={{ flexDirection: 'row', alignItems: 'flex-start', gap: 8 }}><Icon name="lock" size={16} color={colors.muted}/><Txt size={13} color={colors.muted} style={{ flex: 1 }}>{prerequisite(item.id)}</Txt></View>}
        {item.completed && <Txt size={13} color={colors.muted}>Повтор поможет разобраться. Первый ответ в исследовании останется прежним.</Txt>}
      </View>}
    </View>;
  }
  function simulatorCard(item: Simulation, full = false) {
    const isPlan = item.id === 'plan';
    const bg = isPlan ? '#DDEAFF' : '#ECE4FF';
    const ink = isPlan ? '#315B9C' : '#72549C';
    const showFacts = full && expandedSimulation === item.id;
    return <View key={item.id} style={{ backgroundColor: bg, borderRadius: 27, padding: 20, gap: 15, overflow: 'hidden' }}>
      <View style={[s.between, { gap: 8, flexWrap: 'wrap' }]}><Chip label={item.completed ? 'РАЗОБРАНО' : item.unlocked ? 'МОЖНО ПРОБОВАТЬ' : 'ВПЕРЕДИ'} icon={item.completed ? 'check' : item.unlocked ? 'spark' : 'lock'} color="#FFFFFFA8" textColor={ink}/><Txt size={11} color={ink}>3 минуты</Txt></View>
      <View style={{ gap: 8 }}>{full && isPlan && <Image source={require('../assets/headphones.png')} resizeMode="contain" style={{ width: 66, height: 68, alignSelf: 'flex-end', marginBottom: -18 }}/>}<Txt size={22} weight="extra" style={{ lineHeight: 29, letterSpacing: -.5 }}>{item.title}</Txt><Txt size={13} color={ink} style={{ lineHeight: 21 }}>{isPlan ? 'Меняй взнос и срок. Проверь, хватит ли на цель и повседневную жизнь.' : 'Поступлений меньше, а необходимое осталось. Реши, что изменить в этом месяце.'}</Txt></View>
      {full && <Pressable accessibilityRole="button" accessibilityLabel={`Условия симуляции: ${item.title}`} accessibilityState={{ expanded: showFacts, disabled: busy }} aria-expanded={showFacts} disabled={busy} onPress={() => setExpandedSimulation(showFacts ? null : item.id)} style={{ minHeight: 44, flexDirection: 'row', alignItems: 'center', gap: 8 }}><Txt size={13} weight="bold" color={ink} style={{ flex: 1 }}>Условия симуляции</Txt><Icon name={showFacts ? 'close' : 'plus'} size={18} color={ink}/></Pressable>}
      {showFacts && <>
        <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: 8 }}>{item.facts.map(f => <View key={f.label} style={{ width: '48%', backgroundColor: '#FFFFFF9E', padding: 12, borderRadius: 17, gap: 5 }}><Txt size={11} color={ink}>{f.label}</Txt><Txt size={15} weight="extra">{f.value}</Txt></View>)}</View>
        <View style={{ flexDirection: 'row', gap: 8, alignItems: 'center' }}><Icon name="sliders" color={ink} size={17}/><Txt size={13} color={ink} style={{ flex: 1 }}>{isPlan ? 'Выбери сумму → объясни решение → посмотри результат' : 'Выбери взнос → объясни решение → разбери последствия'}</Txt></View>
      </>}
      {item.unlocked
        ? <Button label={item.completed ? (isPlan ? 'Повторить первый план' : 'Повторить сложный месяц') : (isPlan ? 'Собрать первый план' : 'Решить сложный месяц')} onPress={() => onSimulation(item.id)} icon="arrow" variant="white" style={{ minHeight: 49, paddingVertical: 12 }}/>
        : <View style={{ backgroundColor: '#FFFFFF8A', borderRadius: 18, padding: 14, gap: 8 }}><Txt size={13} color={ink}>{isPlan ? 'Откроется после первых трёх историй о бюджете и цели.' : 'Откроется после первой симуляции и трёх историй о переменах.'}</Txt><TinyAction label="Вернуться к нужной главе" color={ink} onPress={() => { const i = isPlan ? 0 : 1; setExpandedChapters(open => open.includes(i) ? open : [...open, i]); setMode('route'); setChapterFilters(current => ({ ...current, [i]: 'all' })); }}/></View>}
      {full && <Txt size={13} color={ink}>Учебные условия. Симуляция не меняет твою личную цель.</Txt>}
    </View>;
  }

  return <ScrollView showsVerticalScrollIndicator={false} keyboardShouldPersistTaps="handled" contentContainerStyle={{ paddingTop: 17, paddingBottom: TAB_BAR_BOTTOM_SPACE }}>
    <View style={{ paddingHorizontal: 22, gap: 14 }}>
      <LinearGradient colors={['#6240B0', '#784BC3', '#A07BDE']} start={{ x: 0, y: 0 }} end={{ x: 1, y: 1 }} style={{ minHeight: 128, borderRadius: 28, padding: 20, overflow: 'hidden', justifyContent: 'center' }}>
        <View style={{ width: '67%', gap: 7, zIndex: 1 }}><Txt accessibilityRole="header" size={28} weight="extra" color="white" style={{ letterSpacing: -.7 }}>Практика</Txt><Txt size={13} color="white" style={{ lineHeight: 21 }}>Пробуй, меняй и находи свой подход.</Txt></View><Mascot size={93} animated={motion} style={{ position: 'absolute', right: -2, bottom: -4 }}/>
      </LinearGradient>
      <View accessibilityRole="tablist" style={{ flexDirection: 'row', gap: 8 }}>
        {([{ id: 'route', label: 'Маршрут', icon: 'compass', color: '#DDEAFF', ink: '#315B9C' }, { id: 'simulators', label: 'Тренажёры', icon: 'sliders', color: '#E8DFFF', ink: '#72549C' }, { id: 'guides', label: 'Памятки', icon: 'book', color: '#E3EDC9', ink: '#4E6C2C' }] as const).map(m => <Pressable key={m.id} disabled={busy} accessibilityRole="tab" accessibilityLabel={`Практика: ${m.label}`} accessibilityState={{ selected: mode === m.id }} aria-selected={mode === m.id} onPress={() => setSection(m.id)} style={{ flex: 1, minHeight: 64, alignItems: 'center', justifyContent: 'center', gap: 6, paddingHorizontal: 3, paddingVertical: 10, borderRadius: 19, borderWidth: 2, borderColor: mode === m.id ? m.ink : 'transparent', backgroundColor: m.color }}><Icon name={m.icon} size={22} color={m.ink}/><Txt size={11} weight="extra" color={m.ink}>{m.label}</Txt></Pressable>)}
      </View>
      {mode === 'route' && <View style={{ backgroundColor: '#E5D8FC', borderRadius: 24, padding: 17, gap: 11 }}>
        <View style={[s.between, { gap: 12 }]}><Txt size={13} weight="bold" color="#69517D">{assessmentReady ? 'Новая ситуация доступна' : allDone ? 'Все шаги разобраны' : 'Следующий шаг'}</Txt><Txt size={11} color="#69517D">{count} из 8</Txt></View>
        <Txt size={21} weight="extra" style={{ lineHeight: 29, letterSpacing: -.4 }}>{nextTitle || 'Применить подход к своей цели'}</Txt>
        <ProgressBar value={count / 8} color="#8A72BB" height={5} background="#D9D1EC"/>
        <Button label={assessmentReady ? 'Пройти новую ситуацию' : allDone ? 'Открыть свою цель' : nextSimulation ? 'Начать симуляцию' : `${count?'Продолжить':'Начать'} · ${nextLesson?.duration || '2 мин'}`} onPress={openNext} style={{ minHeight: 50, paddingVertical: 12 }}/>
      </View>}
    </View>

    {mode === 'route' && <View style={{ paddingHorizontal: 22, gap: 20, marginTop: 24 }}>
      <View style={{ gap: 5 }}><Txt size={21} weight="extra">Все шаги</Txt><Txt size={13} color={colors.muted}>6 историй и 2 симуляции. Можно раскрыть обе главы и повторять пройденное.</Txt></View>
      <View style={{ gap: 10 }}>
        {chapters.map((c, i) => { const expanded = expandedChapters.includes(i), filter = chapterFilters[i] || 'all'; const selectedLessons = state.lessons.slice(i * 3, i * 3 + 3).filter(item => matches(item, filter)); const selectedSimulation = state.simulations[i], visibleSimulation = selectedSimulation && matches(selectedSimulation, filter); return <View key={c.title} style={{ backgroundColor: 'white', borderRadius: 23, paddingHorizontal: 17 }}>
          <Pressable accessibilityRole="button" accessibilityLabel={`Глава ${i + 1}: ${c.title}, ${chapterDone[i]} из 4 шагов разобрано`} accessibilityState={{ expanded }} aria-expanded={expanded} disabled={busy} onPress={() => { setExpandedChapters(open => open.includes(i) ? open.filter(x => x !== i) : [...open, i]); setExpandedLesson(null); }} style={{ minHeight: 82, flexDirection: 'row', alignItems: 'center', gap: 12 }}>
            <View style={{ width: 42, height: 44, borderRadius: 15, backgroundColor: c.color, alignItems: 'center', justifyContent: 'center' }}><Icon name={c.icon} color={c.ink} size={21}/></View>
            <View style={{ flex: 1, gap: 4 }}><Txt size={16} weight="extra">{c.title}</Txt><Txt size={11} color={colors.muted}>Глава {i + 1} · {chapterDone[i]} из 4 шагов</Txt></View><Icon name={expanded ? 'close' : 'plus'} size={18} color={colors.muted}/>
          </Pressable>
          {expanded && <View style={{ paddingBottom: 16 }}>
            <Txt size={13} color={colors.muted}>{c.description}</Txt>
            <Pressable accessibilityRole="button" accessibilityLabel={`Фильтр шагов: ${filter === 'all' ? 'все' : filter === 'available' ? 'доступные' : 'пройденные'}`} accessibilityState={{ expanded: filtersOpen === i }} aria-expanded={filtersOpen === i} disabled={busy} onPress={() => setFiltersOpen(filtersOpen === i ? null : i)} style={{ minHeight: 44, flexDirection: 'row', gap: 7, alignItems: 'center', alignSelf: 'flex-start', marginTop: 5 }}><Icon name="sliders" size={16} color={colors.deepBlue}/><Txt size={13} weight="bold" color={colors.deepBlue}>{filter === 'all' ? 'Фильтр' : filter === 'available' ? 'Доступные шаги' : 'Пройденные шаги'}</Txt></Pressable>
            {filtersOpen === i && <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: 6, marginBottom: 4 }}>{([{ id: 'all', label: 'Все' }, { id: 'available', label: 'Доступные' }, { id: 'completed', label: 'Пройденные' }] as const).map(f => <Pressable key={f.id} accessibilityRole="button" accessibilityLabel={`Фильтр историй: ${f.label}`} accessibilityState={{ selected: filter === f.id }} aria-pressed={filter === f.id} disabled={busy} onPress={() => { setChapterFilters(current => ({ ...current, [i]: f.id })); setExpandedLesson(null); setFiltersOpen(null); }} style={{ minHeight: 44, justifyContent: 'center', paddingHorizontal: 12, paddingVertical: 11, borderRadius: 13, backgroundColor: filter === f.id ? c.color : '#F5F6FA' }}><Txt size={11} weight="bold" color={filter === f.id ? c.ink : colors.muted}>{f.label}</Txt></Pressable>)}</View>}
            {selectedLessons.map(item => lessonRow(item, c))}
            {selectedLessons.length === 0 && !visibleSimulation && <View style={{ paddingVertical: 20, gap: 9 }}><Txt size={15} weight="bold">{filter === 'completed' ? 'Пройденных историй пока нет' : 'Новых доступных шагов нет'}</Txt><Txt size={13} color={colors.muted}>Посмотри все шаги или продолжи с подсказки наверху.</Txt><TinyAction label="Показать все шаги" onPress={() => setChapterFilters(current => ({ ...current, [i]: 'all' }))}/></View>}
            {visibleSimulation && <View style={{ marginTop: 18 }}>{simulatorCard(selectedSimulation)}</View>}
          </View>}
        </View>; })}
      </View>
      <AssessmentPanel state={state} onAssessment={onAssessment} onGoal={onGoal}/>
    </View>}

    {mode === 'simulators' && <View style={{ paddingHorizontal: 22, gap: 19, marginTop: 23 }}>
      <View style={{ gap: 6 }}><Txt size={23} weight="extra" style={{ letterSpacing: -.5 }}>Здесь решения — за тобой</Txt><Txt size={13} color={colors.muted}>Меняй условия, выбирай действие и объясняй, почему оно подходит.</Txt></View>
      {state.simulations.map(item => simulatorCard(item, true))}
      {onLab && <View style={{ backgroundColor: '#E1F0B9', borderRadius: 27, padding: 21, gap: 14 }}><View style={s.between}><Icon name="spark" size={29} color="#5D7835"/><Chip label="БЕЗ ОЦЕНКИ" color="#FFFFFF9E" textColor="#5D7835"/></View><Txt size={23} weight="extra" style={{ letterSpacing: -.5 }}>А если это твой план?</Txt><Txt size={13} color="#546B35">Свободная песочница: попробуй другой взнос, новые расходы или срок на своих цифрах.</Txt><Button label="Проверить свой сценарий" icon="sliders" variant="white" onPress={onLab}/><Txt size={13} color="#546B35">Личный план изменится только после явного сохранения.</Txt></View>}
      <AssessmentPanel state={state} onAssessment={onAssessment} onGoal={onGoal}/>
    </View>}

    {mode === 'guides' && <View style={{ gap: 19, marginTop: 23 }}>
      <View style={{ paddingHorizontal: 22, gap: 15 }}>
        <View style={{ gap: 5 }}><Txt size={23} weight="extra" style={{ letterSpacing: -.5 }}>Полезное под рукой</Txt><Txt size={13} color={colors.muted}>Шесть памяток к историям. Можно заглянуть, когда нужно освежить подход.</Txt></View>
        <View style={{ flexDirection: 'row', alignItems: 'center', backgroundColor: 'white', borderRadius: 19, paddingHorizontal: 15, gap: 9 }}><Icon name="book" color={colors.muted} size={20}/><TextInput value={query} onChangeText={setQuery} editable={!busy} accessibilityLabel="Поиск по памяткам и словарю" placeholder="Например, запас или срок" placeholderTextColor={colors.muted} style={{ flex: 1, minHeight: 53, fontFamily: 'Manrope_500Medium', color: colors.ink, fontSize: 14, paddingVertical: 14 }}/>{query.length > 0 && <Pressable accessibilityRole="button" accessibilityLabel="Очистить поиск памяток" disabled={busy} onPress={() => setQuery('')} style={{ minWidth: 44, minHeight: 44, alignItems: 'center', justifyContent: 'center' }}><Icon name="close" size={18}/></Pressable>}</View>
      </View>
      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ paddingHorizontal: 22, gap: 7 }}>{['Все', 'Бюджет', 'Цель', 'Перемены', 'Безопасность'].map(t => <Pressable key={t} accessibilityRole="button" accessibilityLabel={`Тема памяток: ${t}`} accessibilityState={{ selected: topic === t }} aria-pressed={topic === t} disabled={busy} onPress={() => { setTopic(t); setExpandedGuide(null); }} style={{ minHeight: 44, justifyContent: 'center', paddingHorizontal: 15, paddingVertical: 12, borderRadius: 14, backgroundColor: topic === t ? colors.ink : 'white' }}><Txt size={13} weight="bold" color={topic === t ? 'white' : colors.muted}>{t}</Txt></Pressable>)}</ScrollView>
      <View style={{ paddingHorizontal: 22, gap: 11 }}>
        {guides.map(g => { const expanded = expandedGuide === g.id; const lesson = state.lessons.find(l => l.id === g.id); return <View key={g.id} style={{ backgroundColor: 'white', borderRadius: 25, overflow: 'hidden' }}>
          <Pressable accessibilityRole="button" accessibilityLabel={`Памятка: ${g.title}`} accessibilityState={{ expanded }} aria-expanded={expanded} disabled={busy} onPress={() => setExpandedGuide(expanded ? null : g.id)} style={{ flexDirection: 'row', alignItems: 'center', padding: 17, gap: 12 }}><View style={{ width: 46, height: 50, borderRadius: 17, backgroundColor: g.color, alignItems: 'center', justifyContent: 'center' }}><Icon name={g.icon} size={24} color={g.ink}/></View><View style={{ flex: 1, gap: 5 }}><Txt size={11} color={g.ink}>{g.topic}</Txt><Txt size={15} weight="extra" style={{ lineHeight: 21 }}>{g.title}</Txt></View><Icon name={expanded ? 'close' : 'plus'} size={18} color={colors.muted}/></Pressable>
          {expanded && <View style={{ paddingHorizontal: 18, paddingBottom: 18, gap: 13 }}><Txt size={13} color={colors.muted} style={{ lineHeight: 21 }}>{g.short}</Txt><View style={{ borderRadius: 19, padding: 16, backgroundColor: g.color }}><Txt size={16} weight="bold" color={g.ink} style={{ lineHeight: 24 }}>{g.rule}</Txt></View><Txt size={13} style={{ lineHeight: 22 }}>{g.example}</Txt><View style={{ gap: 6 }}><Txt size={13} weight="extra">Попробуй у себя</Txt><Txt size={13} color={colors.muted} style={{ lineHeight: 21 }}>{g.action}</Txt></View>{lesson?.unlocked ? <TinyAction label={lesson.completed ? 'Повторить связанную историю' : 'Открыть связанную историю'} onPress={() => onLesson(g.id)}/> : <Txt size={13} color={colors.muted}>Связанная история откроется по мере прохождения маршрута.</Txt>}</View>}
        </View>; })}
        {guides.length === 0 && <View style={{ backgroundColor: 'white', borderRadius: 25, padding: 21, gap: 11 }}><Icon name="book" color={colors.muted} size={28}/><Txt size={17} weight="extra">Таких памяток пока нет</Txt><Txt size={13} color={colors.muted}>Попробуй другое слово или открой все темы.</Txt><TinyAction label="Сбросить поиск и тему" onPress={() => { setQuery(''); setTopic('Все'); }}/></View>}
        <View style={{ marginTop: 12, gap: 14 }}><View style={s.between}><Txt size={22} weight="extra" style={{ letterSpacing: -.5 }}>Без сложных слов</Txt><Icon name="help" size={21} color="#8B77B9"/></View><View style={{ backgroundColor: '#EEE8FA', borderRadius: 25, paddingHorizontal: 18 }}>
          {terms.map((t, i) => { const expanded = expandedTerm === t.term; return <View key={t.term} style={{ borderTopWidth: i === 0 ? 0 : 1, borderTopColor: '#DDD4EE' }}><Pressable accessibilityRole="button" accessibilityLabel={`Термин: ${t.term}`} accessibilityState={{ expanded }} aria-expanded={expanded} disabled={busy} onPress={() => setExpandedTerm(expanded ? null : t.term)} style={{ flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 12, minHeight: 56 }}><Txt size={14} weight="bold" style={{ flex: 1 }}>{t.term}</Txt><Icon name={expanded ? 'close' : 'plus'} size={17} color="#76618D"/></Pressable>{expanded && <Txt size={13} color="#68557F" style={{ lineHeight: 21, paddingBottom: 17 }}>{t.meaning}</Txt>}</View>; })}
          {terms.length === 0 && <Txt size={13} color="#68557F" style={{ paddingVertical: 18 }}>В словарике тоже нет совпадений. Попробуй другое слово.</Txt>}
        </View></View>
      </View>
    </View>}
    <View style={{ paddingHorizontal: 25, marginTop: 25 }}><Txt size={13} color={colors.muted} style={{ lineHeight: 21 }}>Пройденный шаг — разобранная ситуация. Количество шагов само по себе не показывает, насколько навык закрепился.</Txt></View>
  </ScrollView>;
}
