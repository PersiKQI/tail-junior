import { TAB_BAR_BOTTOM_SPACE } from './layout';
import React, { useEffect, useState } from 'react';
import { Image, Pressable, ScrollView, View } from 'react-native';
import Svg, { Circle, Line, Path } from 'react-native-svg';
import type { AppState, Checkin, Goal } from '../shared/types';
import { api } from './api';
import { goalMilestones, goalPaces, projectionPoints, type PaceId } from './goal-planner';
import { Button, colors, Disclosure, ErrorNote, formatMoney, Gradient, Icon, IconButton, Mascot, ProgressBar, s, Txt } from './ui';

interface Props {
  state: AppState; motion: boolean; onState: (state: AppState) => void;
  onBusyChange: (busy: boolean) => void; onEdit: () => void;
  onCheckin: (amount?: number) => void; onPractice: () => void;
  onLab: () => void; onActions: () => void; onBudget: () => void;
}

function Projection({ goal, monthly, months, funded }: { goal: Goal; monthly: number; months: number; funded: boolean }) {
  const points = projectionPoints(goal.saved, monthly, months);
  const [selected, setSelected] = useState(points.length - 1);
  useEffect(() => setSelected(projectionPoints(goal.saved, monthly, months).length - 1), [goal.saved, monthly, months]);
  const chosen = points[Math.min(selected, points.length - 1)];
  const max = Math.max(goal.target, points.at(-1)!.amount, 1);
  const plot = points.map((p, i) => ({ x: 14 + p.month / months * 272, y: 136 - p.amount / max * 112 }));
  const line = plot.map((p, i) => `${i ? 'L' : 'M'}${p.x},${p.y}`).join(' ');
  const active = plot[Math.min(selected, plot.length - 1)], color = funded ? '#7360C3' : '#B57E28';
  const yTarget = 136 - goal.target / max * 112;
  return <View style={{ backgroundColor: 'white', borderRadius: 28, padding: 20, gap: 15 }}>
    <View style={s.between}><View style={{ flex: 1, gap: 4 }}><Txt size={20} weight="extra">Как растёт твой план</Txt><Txt size={13} color={colors.muted}>От текущей суммы, без процентов</Txt></View><Icon name="growth" color={color} /></View>
    <View style={s.between}><View><Txt size={27} weight="extra">{formatMoney(chosen.amount)}</Txt><Txt size={13} color={colors.muted}>{chosen.month === 0 ? 'Уже отмечено тобой' : `Через ${chosen.month} мес. при выбранном взносе`}</Txt></View></View>
    <View accessibilityRole="image" accessibilityLabel={`Прогноз: сейчас ${formatMoney(goal.saved)}, через ${months} мес. ${formatMoney(points.at(-1)!.amount)}. Цель ${formatMoney(goal.target)}.`}>
      <Svg width="100%" height={152} viewBox="0 0 300 152" aria-hidden={true}>
        <Line x1={14} y1={136} x2={286} y2={136} stroke="#EEF0F6" />
        <Path d={`${line} L286,136 L14,136 Z`} fill={funded ? '#EEE9FA' : '#FFF0D7'} />
        <Line x1={14} y1={yTarget} x2={286} y2={yTarget} stroke="#BEACDF" strokeDasharray="4 5" />
        <Path d={line} fill="none" stroke={color} strokeWidth={3} strokeLinecap="round" strokeLinejoin="round" />
        <Line x1={active.x} y1={active.y} x2={active.x} y2={136} stroke={color} strokeDasharray="3 4" opacity={0.4} />
        {plot.map((p, i) => <Circle key={i} cx={p.x} cy={p.y} r={i === selected ? 6 : 3.5} fill={i === selected ? color : 'white'} stroke={color} strokeWidth={2} />)}
      </Svg>
    </View>
    <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 6 }}>{points.map((p, i) => <Pressable key={p.month} accessibilityRole="button" accessibilityLabel={`Прогноз: ${p.month === 0 ? 'сейчас' : `через ${p.month} мес.`}`} accessibilityState={{ selected: selected === i }} aria-pressed={selected === i} onPress={() => setSelected(i)} style={{ minWidth: 50, minHeight: 44, paddingHorizontal: 10, borderRadius: 14, alignItems: 'center', justifyContent: 'center', backgroundColor: selected === i ? '#EEE8FA' : colors.bg }}><Txt size={13} weight="bold" color={selected === i ? '#6749A6' : colors.muted}>{p.month === 0 ? 'Сейчас' : `${p.month} мес.`}</Txt></Pressable>)}</ScrollView>
    <View style={{ flexDirection: 'row', gap: 7, alignItems: 'center' }}><View style={{ width: 17, height: 2, backgroundColor: '#BEACDF' }} /><Txt size={13} color={colors.muted}>Линия цели · {formatMoney(goal.target)}</Txt></View>
    <Txt size={13} color={funded ? colors.muted : '#8A5D1F'}>{!funded ? 'Этот расчёт пока не обеспечен бюджетом. Уменьши взнос или пересмотри расходы.' : chosen.amount >= goal.target ? 'При этих условиях сумма цели будет собрана. Цены и поступления ещё могут измениться.' : `До цели останется ${formatMoney(goal.target - chosen.amount)}.`}</Txt>
  </View>;
}

function History({ checkins, onCheckin }: { checkins: Checkin[]; onCheckin: (amount?: number) => void }) {
  const [type, setType] = useState<'all' | 'saved' | 'reviewed'>('all');
  const [period, setPeriod] = useState<7 | 30 | 0>(0);
  const [limit, setLimit] = useState(10);
  const filtered = checkins.filter(c => (type === 'all' || c.type === type) && (period === 0 || new Date(c.createdAt).getTime() >= Date.now() - period * 86400000));
  const total = filtered.filter(c => c.type === 'saved').reduce((sum, c) => sum + (c.amount || 0), 0);
  function reset() { setLimit(10); }
  return <View style={{ gap: 19 }}>
    <View style={{ backgroundColor: '#E7E0FB', borderRadius: 27, padding: 21, gap: 10 }}><View style={s.between}><Txt size={17} weight="extra">Шаги, которые ты отметил</Txt><Icon name="doubleCheck" color="#7250AE" /></View><Txt size={29} weight="extra">{formatMoney(total)}</Txt><Txt size={13} color="#65537F">Отложено по выбранным записям · отметок: {filtered.length}</Txt></View>
    <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 7 }}>{([{ id: 'all', label: 'Все шаги' }, { id: 'saved', label: 'Взносы' }, { id: 'reviewed', label: 'Пересмотр плана' }] as const).map(x => <Pressable key={x.id} accessibilityRole="button" accessibilityState={{ selected: type === x.id }} aria-pressed={type === x.id} onPress={() => { setType(x.id); reset(); }} style={{ paddingHorizontal: 15, paddingVertical: 12, borderRadius: 15, backgroundColor: type === x.id ? colors.ink : 'white' }}><Txt size={13} weight="bold" color={type === x.id ? 'white' : colors.muted}>{x.label}</Txt></Pressable>)}</ScrollView>
    <View style={{ flexDirection: 'row', gap: 14 }}>{([{ days: 0, label: 'Все записи' }, { days: 7, label: '7 дней' }, { days: 30, label: '30 дней' }] as const).map(x => <Pressable key={x.days} accessibilityRole="button" aria-pressed={period === x.days} accessibilityState={{ selected: period === x.days }} onPress={() => { setPeriod(x.days); reset(); }} style={{ minHeight: 44, justifyContent: 'center', borderBottomWidth: 2, borderBottomColor: period === x.days ? colors.blue : 'transparent' }}><Txt size={13} weight="bold" color={period === x.days ? colors.blue : colors.muted}>{x.label}</Txt></Pressable>)}</View>
    {filtered.length === 0 ? <View style={{ backgroundColor: 'white', borderRadius: 27, padding: 24, gap: 15 }}><Icon name="flag" size={30} color={colors.blue} /><Txt size={21} weight="extra">Здесь будет твой первый шаг</Txt><Txt size={13} color={colors.muted}>{checkins.length ? 'В этом фильтре записей нет. Выбери другой период или тип действия.' : 'Отметь, когда отложишь деньги или проверишь план. Небольшие шаги тоже считаются.'}</Txt><Button label="Отметить свой шаг" onPress={() => onCheckin()} /></View> : <View style={{ backgroundColor: 'white', borderRadius: 27, padding: 20 }}>{filtered.slice(0, limit).map((c, i) => <View key={c.id} style={{ flexDirection: 'row', gap: 12, paddingVertical: 15, borderBottomWidth: i < Math.min(filtered.length, limit) - 1 ? 1 : 0, borderBottomColor: colors.line }}><View style={{ width: 40, height: 40, borderRadius: 15, alignItems: 'center', justifyContent: 'center', backgroundColor: c.type === 'saved' ? colors.mint : colors.lavender }}><Icon name={c.type === 'saved' ? 'plus' : 'refresh'} color={c.type === 'saved' ? colors.green : '#7150B2'} size={19} /></View><View style={{ flex: 1, gap: 4 }}><Txt size={16} weight="extra">{c.type === 'saved' ? `+${formatMoney(c.amount || 0)}` : 'План проверен'}</Txt>{!!c.note && <Txt size={13} color={colors.muted}>{c.note}</Txt>}<Txt size={13} color={colors.muted}>{new Date(c.createdAt).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long' })} · твоя отметка</Txt></View></View>)}{filtered.length > limit && <Button label="Показать ещё" variant="secondary" icon="plus" onPress={() => setLimit(limit + 10)} />}</View>}
    <Txt size={13} color={colors.muted}>История хранит до 100 последних отметок. Это история сообщений, не банковская выписка. Накопленная сумма может отличаться, если ты исправлял её в настройках цели.</Txt>
  </View>;
}

export function GoalScreen({state,motion,onState,onBusyChange,onEdit,onCheckin,onPractice,onLab,onActions,onBudget}:Props){
 const goal=state.goal;
 const [pace,setPace]=useState<PaceId>('current');
 const [milestone,setMilestone]=useState<number|null>(null);
 const [busy,setBusy]=useState(false),[error,setError]=useState<string|null>(null),[notice,setNotice]=useState<string|null>(null);
 useEffect(()=>{setPace('current');setMilestone(null);},[goal?.updatedAt]);
 if(!goal)return <ScrollView contentContainerStyle={{padding:20,paddingBottom:TAB_BAR_BOTTOM_SPACE,gap:22}}><View style={{backgroundColor:'white',borderRadius:24,padding:24,gap:18}}><Mascot size={78} animated={motion}/><Txt size={25} weight="extra">На что хочешь накопить?</Txt><Txt size={15} color={colors.muted}>Укажи сумму и срок. Посчитаем, сколько нужно откладывать.</Txt><Button label="Создать цель" onPress={onEdit}/></View><Txt size={13} color={colors.muted}>Цель можно добавить позже. Уроки и виртуальный портфель уже доступны.</Txt><Button label="Вернуться к практике" variant="secondary" onPress={onPractice}/></ScrollView>;
 const variants=goalPaces(goal),chosen=variants.find(item=>item.id===pace)!;
 const reached=goal.saved>=goal.target,pct=Math.min(1,goal.saved/goal.target);
 const budgetConfigured=state.budget.income>0||state.budget.categories.some(item=>item.amount>0)||state.budget.reserve>0;
 const funded=budgetConfigured&&chosen.forecast.budgetBalance>=0&&chosen.monthly<=chosen.forecast.available;
 const steps=goalMilestones(goal,chosen.monthly),activeStep=steps.find(item=>item.percent===milestone)||steps.find(item=>!item.reached)||steps[3];
 const changed=chosen.monthly!==goal.monthly||chosen.months!==goal.months;
 async function savePace(){if(busy||!chosen.forecast.feasible)return;setBusy(true);onBusyChange(true);setError(null);setNotice(null);try{onState(await api.goal({...goal!,monthly:chosen.monthly,months:chosen.months,categories:state.budget.categories}));setNotice('Новый темп сохранён. Бюджет обновлён.');}catch(e){setError((e as Error).message);}finally{setBusy(false);onBusyChange(false);}}
 return <ScrollView showsVerticalScrollIndicator={false} contentContainerStyle={{padding:20,paddingTop:8,paddingBottom:TAB_BAR_BOTTOM_SPACE,gap:24}}>
  <View style={{backgroundColor:'white',borderRadius:26,overflow:'hidden'}}>
   <Gradient style={{padding:22,minHeight:146}}><View style={{width:'67%',gap:10,zIndex:1}}><Txt size={26} weight="extra" color="white" style={{lineHeight:34,letterSpacing:-.5}}>{goal.title}</Txt><View style={{alignSelf:'flex-start',paddingHorizontal:10,paddingVertical:6,borderRadius:10,backgroundColor:'#FFFFFFEC'}}><Txt size={14} weight="bold" color={colors.ink}>{formatMoney(goal.saved)} из {formatMoney(goal.target)}</Txt></View></View>{goal.title.toLowerCase().includes('наушник')?<Image source={require('../assets/headphones.png')} resizeMode="contain" style={{position:'absolute',width:108,height:108,right:0,bottom:2}}/>:<Mascot size={96} animated={motion} style={{position:'absolute',right:0,bottom:0}}/>}</Gradient>
   <View style={{padding:20,gap:16}}><View style={s.between}><Txt size={15} weight="bold">{reached?'Цель достигнута':`Ещё ${formatMoney(goal.target-goal.saved)}`}</Txt><Txt size={14} weight="bold" color={colors.deepBlue}>{Math.round(pct*100)}%</Txt></View><ProgressBar value={pct} height={7}/><Txt size={14} color={colors.muted}>План: {formatMoney(goal.monthly)} в месяц · {goal.months} мес.</Txt>
   <Button label={reached?'Создать следующий план':'Добавить накопление'} icon={reached?'flag':'plus'} onPress={reached?onEdit:()=>onCheckin()}/>
   <Pressable accessibilityRole="button" accessibilityLabel="Изменить цель" onPress={onEdit} style={{minHeight:44,justifyContent:'center',alignItems:'center'}}><Txt size={14} weight="bold" color={colors.deepBlue}>Изменить сумму и срок</Txt></Pressable></View>
  </View>
  {!reached&&<View style={{gap:10}}><Txt size={14} weight="bold">Быстро добавить</Txt><View style={{flexDirection:'row',gap:8}}>{[100,500,1000].map(amount=><Pressable key={amount} accessibilityRole="button" accessibilityLabel={`Отметить взнос ${amount} рублей`} onPress={()=>onCheckin(amount)} style={{flex:1,minHeight:48,borderRadius:16,backgroundColor:colors.lavender,alignItems:'center',justifyContent:'center'}}><Txt size={14} weight="bold">+{formatMoney(amount)}</Txt></Pressable>)}</View><Txt size={12} color={colors.muted}>Сумму можно проверить перед сохранением.</Txt></View>}
  {!budgetConfigured?<View style={{gap:8}}><Txt size={14} color={colors.muted}>Чтобы проверить, подходит ли взнос твоему бюджету, укажи поступления и расходы.</Txt><Pressable accessibilityRole="button" onPress={onBudget} style={{minHeight:44,justifyContent:'center'}}><Txt size={14} weight="bold" color={colors.deepBlue}>Настроить бюджет →</Txt></Pressable></View>:!goal.feasible&&<View style={{padding:16,borderRadius:18,backgroundColor:colors.yellow,gap:8}}><Txt size={14} weight="bold">План нужно пересмотреть</Txt><Txt size={13}>Проверь сумму, срок и доступный месячный взнос.</Txt><Pressable accessibilityRole="button" onPress={onEdit} style={{minHeight:44,justifyContent:'center'}}><Txt size={14} weight="bold" color={colors.deepBlue}>Изменить план →</Txt></Pressable></View>}
  {!reached&&<Disclosure title="Изменить темп накоплений" summary={`${formatMoney(goal.monthly)} в месяц · ${goal.months} мес.`}>
   <Txt size={14} color={colors.muted}>Сравни варианты. Изменения применятся после сохранения.</Txt>
   {variants.map(item=><Pressable key={item.id} disabled={busy} accessibilityRole="radio" accessibilityLabel={`Темп: ${item.title}`} accessibilityState={{checked:pace===item.id,disabled:busy}} aria-checked={pace===item.id} onPress={()=>{setPace(item.id);setNotice(null);setError(null);}} style={{minHeight:76,padding:16,borderRadius:16,backgroundColor:pace===item.id?colors.paleBlue:'white',flexDirection:'row',alignItems:'center',gap:12}}><Icon name={pace===item.id?'success':'clock'} color={pace===item.id?colors.deepBlue:colors.muted}/><View style={{flex:1,gap:4}}><Txt size={15} weight="bold">{item.title}</Txt><Txt size={13} color={colors.muted}>{formatMoney(item.monthly)} в месяц · {item.months} мес.</Txt></View></Pressable>)}
   <Txt size={14} color={colors.muted}>{!budgetConfigured?'Посильность пока не проверена: сначала заполни бюджет.':chosen.forecast.budgetBalance<0?'Обязательные расходы и запас выше поступлений. Сначала пересмотри бюджет.':chosen.monthly>goal.available?`Для цели доступно ${formatMoney(goal.available)} в месяц.`:chosen.forecast.gap>0?`За этот срок не хватает ${formatMoney(chosen.forecast.gap)}.`:`После взноса остаётся ${formatMoney(goal.available-chosen.monthly)} сверх расходов и запаса.`}</Txt>
   <ErrorNote message={error}/>{notice&&<Txt accessibilityRole="alert" size={14} color={colors.green}>{notice}</Txt>}
   {changed&&<Button label="Сохранить этот темп" onPress={savePace} loading={busy} disabled={!chosen.forecast.feasible}/>}
   <Button label="Указать свой срок и взнос" variant="secondary" onPress={onEdit}/>
  </Disclosure>}
  <Disclosure title="Прогноз и этапы" summary="Как меняется сумма по твоему плану">
   <Projection goal={goal} monthly={chosen.monthly} months={chosen.months} funded={funded}/>
   <Txt size={17} weight="bold">Этапы цели</Txt>
   <View style={{flexDirection:'row',gap:6}}>{steps.map(step=><Pressable key={step.percent} accessibilityRole="button" accessibilityLabel={`Этап ${step.percent} процентов`} accessibilityState={{selected:activeStep.percent===step.percent}} aria-pressed={activeStep.percent===step.percent} onPress={()=>setMilestone(step.percent)} style={{flex:1,minHeight:52,borderRadius:14,backgroundColor:activeStep.percent===step.percent?colors.paleBlue:'white',alignItems:'center',justifyContent:'center'}}><Txt size={15} weight="bold">{step.percent}%</Txt></Pressable>)}</View>
   <Txt size={14} color={colors.muted}>{activeStep.reached?`Этап ${activeStep.percent}% уже отмечен.`:`Ещё ${formatMoney(activeStep.remaining)} до ${activeStep.percent}% цели.${activeStep.months===null?' Взнос пока не определён.':` По выбранному взносу — примерно ${activeStep.months} мес.`}`}</Txt>
  </Disclosure>
  <Disclosure title="История накоплений" summary={`${state.checkins.length} записей`}><History checkins={state.checkins} onCheckin={onCheckin}/></Disclosure>
  <Disclosure title="План недели и другие сценарии"><Button label="Запланировать действие" variant="secondary" icon="calendar" onPress={onActions}/><Button label="Проверить: что, если?" variant="secondary" icon="spark" onPress={onLab}/></Disclosure>
  <Txt size={13} color={colors.muted}>Здесь твои записи о накоплениях. Приложение не переводит деньги и не проверяет банковский баланс.</Txt>
 </ScrollView>;
}
