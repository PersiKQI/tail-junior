import React, { useState } from 'react';
import { Pressable, ScrollView, View } from 'react-native';
import type { AppState, AnswerResult, Choice } from '../shared/types';
import { api } from './api';
import { Button, Card, Chip, colors, ErrorNote, Icon, IconButton, Mascot, ProgressBar, s, Txt } from './ui';

export type ExerciseRoute={kind:'lesson'|'simulation'|'assessment';id:string};
export function Exercise({route,state,motion,onState,onClose,onContinue,onBusyChange}:{onBusyChange:(v:boolean)=>void;route:ExerciseRoute;state:AppState;motion:boolean;onState:(s:AppState)=>void;onClose:()=>void;onContinue:(s:AppState)=>void}){
  const [selected,setSelected]=useState<string|null>(null);const [reason,setReason]=useState<string|null>(null);const [stage,setStage]=useState(0);const [result,setResult]=useState<AnswerResult|null>(null);const [busy,setBusy]=useState(false);const [error,setError]=useState<string|null>(null);
  const lesson=state.lessons.find(x=>x.id===route.id);const sim=state.simulations.find(x=>x.id===route.id);
  const item=route.kind==='lesson'?lesson:route.kind==='simulation'?sim:state.assessment;
  if(!item)return <View style={s.page}><Txt>Эта история недоступна.</Txt><Button label="К маршруту" onPress={onClose}/></View>;
  const isMulti=route.kind!=='lesson';const reasons=route.kind==='simulation'?sim?.reasons:state.assessment.reasons;
  const choices:Choice[]=stage===1?(reasons||[]):item.choices||[];
  async function submit(){if(!selected)return;if(isMulti&&stage===0){setStage(1);return;}if(isMulti&&!reason)return;setBusy(true);onBusyChange(true);setError(null);try{const r=route.kind==='lesson'?await api.answer(route.id,selected):route.kind==='simulation'?await api.simulate(route.id,selected,reason!):await api.assess(selected,reason!);setResult(r);onState(r.state);}catch(e){setError((e as Error).message);}finally{setBusy(false);onBusyChange(false);}}
  if(result)return <ScrollView contentContainerStyle={[s.page,{flexGrow:1}]} showsVerticalScrollIndicator={false}>
    <View style={s.between}><Chip label={result.correct?'РЕШЕНИЕ НАЙДЕНО':'РАЗБЕРЁМСЯ ВМЕСТЕ'} color={result.correct?colors.mint:colors.yellow} textColor={result.correct?colors.green:'#755315'}/><IconButton name="close" label="Закрыть разбор" onPress={onClose}/></View>
    <View style={{alignItems:'center',paddingTop:12}}><View style={{position:'absolute',top:25,width:190,height:190,borderRadius:95,backgroundColor:result.correct?colors.mint:colors.paleBlue}}/><Mascot size={172} animated={motion}/></View>
    <View style={{gap:13}}><Txt size={31} weight="extra" style={{letterSpacing:-.9,lineHeight:39}}>{result.title}</Txt><Txt size={16} color={colors.muted} style={{lineHeight:26}}>{result.correct?'Ты нашёл опору для решения. Давай закрепим, почему оно работает.':'Это учебная ситуация — здесь можно пробовать. Посмотри, что стоит учесть.'}</Txt></View>
    <Card style={{gap:12}}><View style={[s.row,{gap:9}]}><Icon name="spark" color={colors.blue}/><Txt weight="bold">Что взять с собой</Txt></View><Txt size={16} style={{lineHeight:27}}>{result.explanation}</Txt></Card>
    <Txt color={colors.muted} size={12}>{result.alreadyCompleted?'Ты повторил уже пройденный шаг. Первый ответ в статистике сохранён.':'Ответ сохранён. Можно двигаться дальше или вернуться к разбору позже.'}</Txt>
    <View style={{flex:1}}/><Button label={route.kind==='assessment'?'К своему результату':'Продолжить путь'} onPress={()=>onContinue(result.state)}/><Button label="К маршруту" icon={null} variant="secondary" onPress={onClose}/>
  </ScrollView>;
  return <ScrollView key={stage} contentContainerStyle={[s.page,{flexGrow:1}]} keyboardShouldPersistTaps="handled" showsVerticalScrollIndicator={false}>
    <View style={[s.row,{gap:16}]}><IconButton name="back" label={stage===1?'Вернуться к выбору решения':'Вернуться к маршруту'} onPress={()=>stage===1?setStage(0):onClose()}/><View style={{flex:1}}><ProgressBar value={isMulti?(stage+1)/2:(lesson?.order||1)/6}/></View><Txt size={12} weight="bold" color={colors.muted}>{isMulti?`${stage+1} / 2`:`${lesson?.order} / 6`}</Txt></View>
    <View style={{gap:12}}><Chip label={route.kind==='lesson'?lesson?.category||'ПРАКТИКА':route.kind==='simulation'?'СИМУЛЯЦИЯ':'ПРОВЕРКА ЧЕРЕЗ НЕДЕЛЮ'} icon={isMulti?'spark':'book'}/><Txt size={30} weight="extra" style={{letterSpacing:-1,lineHeight:38}}>{stage===1?'Почему именно так?':item.question}</Txt>{stage===1&&<Txt color={colors.muted}>Выбери объяснение своего решения.</Txt>}</View>
    {stage===0&&<Card style={{gap:16,backgroundColor:colors.paleBlue}}><Txt size={16} style={{lineHeight:27}}>{item.context}</Txt>{route.kind==='simulation'&&sim?.facts?.length? <View style={{gap:10,paddingTop:12,borderTopWidth:1,borderTopColor:'#D6E1F8'}}>{sim.facts.map(f=><View key={f.label} style={s.between}><Txt color={colors.muted} size={13} style={{flex:1}}>{f.label}</Txt><Txt weight="extra" size={16}>{f.value}</Txt></View>)}</View>:null}</Card>}
    <View style={{gap:11}}>{choices.map((choice,i)=>{const checked=(stage===1?reason:selected)===choice.id;return <Pressable key={choice.id} disabled={busy} accessibilityRole="radio" accessibilityState={{checked}} aria-checked={checked} accessibilityLabel={choice.text} onPress={()=>stage===1?setReason(choice.id):setSelected(choice.id)} style={{minHeight:76,flexDirection:'row',alignItems:'center',gap:13,padding:17,borderRadius:22,borderWidth:2,borderColor:checked?colors.blue:'transparent',backgroundColor:checked?'#EAF1FF':'white'}}><View style={{width:28,height:28,borderRadius:14,borderWidth:checked?0:1,borderColor:'#CFD6E4',alignItems:'center',justifyContent:'center',backgroundColor:checked?colors.blue:'transparent'}}>{checked?<Icon name="check" color="white" size={17}/>:<Txt size={12} weight="bold" color={colors.muted}>{i+1}</Txt>}</View><Txt size={15} weight={checked?'bold':'medium'} style={{flex:1,lineHeight:23}}>{choice.text}</Txt></Pressable>;})}</View>
    <ErrorNote message={error}/><View style={{flex:1}}/>
    <Button label={isMulti&&stage===0?'Объяснить выбор':'Проверить решение'} onPress={submit} disabled={stage===1?!reason:!selected} loading={busy}/><Txt size={11} color={colors.muted} style={{textAlign:'center'}}>Учебная история. Здесь нет операций с деньгами.</Txt>
  </ScrollView>;
}
