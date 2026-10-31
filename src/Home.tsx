import React from 'react';
import { Image, Pressable, ScrollView, View, useWindowDimensions } from 'react-native';
import { LinearGradient } from 'expo-linear-gradient';
import type { AppState } from '../shared/types';
import { getCharacter } from './characters';
import { TAB_BAR_BOTTOM_SPACE } from './layout';
import { FocusPanel, WeekPanel } from './Dashboard';
import { Button, colors, Disclosure, formatMoney, Icon, Mascot, ProgressBar, Txt, type IconName } from './ui';

type Props={onCharacter:()=>void;onInvestments:(lessonId?:string)=>void;state:AppState;motion:boolean;onState:(s:AppState)=>void;onBusyChange:(b:boolean)=>void;onPractice:()=>void;onNext:()=>void;onGoal:()=>void;onProfile:()=>void;onAssessment:()=>void;onBudget:()=>void;onLab:()=>void;onActions:()=>void;onCheckin:()=>void};
export function Home({state,motion,onInvestments,onPractice,onGoal,onAssessment,onState,onBusyChange,onNext,onActions,onBudget,onLab,onCheckin,onCharacter}:Props){
 const {profile,goal,investments}=state;const narrow=useWindowDimensions().width<360;const character=getCharacter(profile.character);
 const lesson=investments.lessons.find(item=>item.id===investments.progress.nextLessonId);
 const completed=investments.progress.completedLessons;
 const name=profile.name?.trim(),focus=profile.focus;
 const isGoal=focus==='goal',isBudget=focus==='gentle';
 const title=isGoal?(goal?`Ещё ${formatMoney(Math.max(0,goal.target-goal.saved))} до цели`:'У твоей мечты будет план'):isBudget?'Посмотрим, на что хватает':lesson?.title||'Проверим идеи в портфеле';
 const detail=isGoal?(goal?`${goal.title} · по ${formatMoney(goal.monthly)} в месяц`:'Начни с суммы и срока. Остальное посчитаем вместе.'):isBudget?'Поступления, расходы и свободные деньги — в одном месте.':lesson?.subtitle||'Один состав, разные условия рынка. Попробуй на виртуальных деньгах.';
 const action=isGoal?(goal?onCheckin:onGoal):isBudget?onBudget:()=>onInvestments(lesson?.id);
 const cta=isGoal?(goal?'Добавить накопление':'Создать свою цель'):isBudget?'Открыть мой бюджет':lesson?(completed?'Продолжить уроки':'Начать первый урок'):'Открыть портфель';
 const shortcuts:{title:string;icon:IconName;color:string;action:()=>void}[]=[{title:'Накопления',icon:'plus',color:'#E3EEFF',action:onCheckin},{title:'Что, если?',icon:'spark',color:'#E9DCFF',action:onLab},{title:'Бюджет',icon:'wallet',color:'#E5F3B4',action:onBudget},{title:'План недели',icon:'calendar',color:'#FFE8A6',action:onActions}];
 return <ScrollView showsVerticalScrollIndicator={false} contentContainerStyle={{paddingBottom:TAB_BAR_BOTTOM_SPACE,gap:24}}>
  <LinearGradient colors={['#2758D1','#3887F4','#8FCBF8']} locations={[0,.65,1]} start={{x:0,y:0}} end={{x:1,y:1}} style={{marginHorizontal:16,marginTop:8,padding:22,borderRadius:30,minHeight:230,overflow:'hidden'}}>
   <View style={{width:narrow?148:185,gap:12,zIndex:1,paddingTop:8}}><Txt size={13} color="white">{name&&name!=='Друг'?`Привет, ${name}!`:'Привет, исследователь!'}</Txt><Txt size={narrow?24:28} weight="extra" color="white" style={{lineHeight:narrow?32:36,letterSpacing:-.6}}>Твои планы.{ '\n'}Твой темп.</Txt></View>
   <Pressable accessibilityRole="button" accessibilityLabel={`Сменить персонажа: ${character.name}`} onPress={onCharacter} style={{position:'absolute',width:narrow?166:190,height:218,right:narrow?-5:2,bottom:-4,alignItems:'center',justifyContent:'flex-end'}}><Mascot character={character.id} size={narrow?174:190} animated={motion}/></Pressable>
   <Pressable accessibilityRole="button" accessibilityLabel="Выбрать другого персонажа" onPress={onCharacter} style={{alignSelf:'flex-start',marginTop:24,minHeight:44,paddingHorizontal:12,borderRadius:16,backgroundColor:'#FFFFFFEC',flexDirection:'row',gap:5,alignItems:'center',zIndex:2}}><Icon name="spark" size={15} color={colors.deepBlue}/><Txt size={12} weight="bold" color={colors.deepBlue}>{character.name}</Txt><Icon name="chevron" size={14} color={colors.deepBlue}/></Pressable>
  </LinearGradient>
  <View style={{paddingHorizontal:20,gap:24}}>
   <View style={{gap:12}}><View style={{flexDirection:'row',alignItems:'center',justifyContent:'space-between'}}><Txt size={21} weight="extra">Сейчас</Txt><Txt size={12} color={colors.muted}>{isGoal?'Личная цель':isBudget?'Мои деньги':`Инвестиции · ${lesson?.duration||'практика'}`}</Txt></View>
    <View style={{backgroundColor:isGoal?'#E7F2CB':isBudget?'#FFF0C9':'#E8DEFF',borderRadius:26,padding:20,gap:14}}><Txt size={23} weight="extra" style={{lineHeight:31,letterSpacing:-.4}}>{title}</Txt><Txt size={14} color={colors.ink} style={{lineHeight:22}}>{detail}</Txt><Button label={cta} onPress={action}/></View>
   </View>
   <View style={{flexDirection:'row',gap:6}}>{shortcuts.map(item=><Pressable key={item.title} accessibilityRole="button" accessibilityLabel={item.title} onPress={item.action} style={{flex:1,minHeight:80,alignItems:'center',gap:9}}><View style={{width:48,height:48,borderRadius:17,backgroundColor:item.color,alignItems:'center',justifyContent:'center'}}><Icon name={item.icon} size={23}/></View><Txt size={11} weight="bold" style={{textAlign:'center'}}>{item.title}</Txt></Pressable>)}</View>
   <FocusPanel compact state={state} onState={onState} onBusyChange={onBusyChange} onNext={onNext} onActions={onActions} onBudget={onBudget} onPractice={onPractice}/>
   {goal&&<View style={{gap:12}}><Txt size={21} weight="extra">Твой план</Txt><Pressable accessibilityRole="button" accessibilityLabel="Открыть мою цель" onPress={onGoal} style={{backgroundColor:'white',borderRadius:24,padding:20,gap:14}}><View style={{flexDirection:'row',gap:12,alignItems:'center'}}><View style={{flex:1,gap:8}}><Txt size={21} weight="extra">{goal.title}</Txt><Txt size={18} weight="extra">{formatMoney(goal.saved)}</Txt><Txt size={13} color={colors.muted}>из {formatMoney(goal.target)}</Txt></View>{goal.title.toLowerCase().includes('наушник')?<Image source={require('../assets/headphones.png')} resizeMode="contain" style={{width:88,height:88}}/>:<View style={{width:72,height:72,borderRadius:23,backgroundColor:colors.yellow,alignItems:'center',justifyContent:'center'}}><Icon name="flag" size={33}/></View>}</View><ProgressBar value={goal.saved/goal.target} height={6} color="#9A7AE4"/><View style={{flexDirection:'row',alignItems:'center',justifyContent:'space-between'}}><Txt size={13} color={colors.muted}>По плану: {formatMoney(goal.monthly)} в месяц</Txt><Icon name="chevron" size={18}/></View></Pressable></View>}
   <Disclosure title="Твоя неделя" summary="Календарь, планы и сделанные шаги"><WeekPanel hideTitle state={state} onActions={onActions} onGoal={onGoal}/></Disclosure>
   <View style={{gap:12}}><Txt size={21} weight="extra">Твои маршруты</Txt>{[{label:'Инвестиции',detail:`${completed} из 6 историй`,color:'#E8DEFF',icon:'chart' as const,action:()=>onInvestments()},{label:'Практика накоплений',detail:`${state.progress.completedLessons+state.progress.completedSimulations} из 8 шагов`,color:'#FFE9BC',icon:'compass' as const,action:onPractice}].map(item=><Pressable key={item.label} accessibilityRole="button" accessibilityLabel={`Открыть: ${item.label}`} onPress={item.action} style={{minHeight:76,backgroundColor:item.color,borderRadius:20,padding:16,flexDirection:'row',gap:12,alignItems:'center'}}><Icon name={item.icon} size={25}/><View style={{flex:1,gap:4}}><Txt size={16} weight="bold">{item.label}</Txt><Txt size={13} color={colors.muted}>{item.detail}</Txt></View><Icon name="chevron" size={18}/></Pressable>)}</View>
   {state.assessment.status==='available'&&<Button label="Проверить себя через неделю" variant="secondary" onPress={onAssessment}/>}
   <Txt size={13} color={colors.muted} style={{lineHeight:21}}>Уроки и портфель — на учебных деньгах. Свою цель можно вести отдельно, без подключения банка.</Txt>
  </View>
 </ScrollView>;
}
